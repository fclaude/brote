package scheme

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// This file is what a host can do with tasks and channels from Go: the
// handles it gets from a Value, detached tasks and timers, and Shutdown. None
// of it takes part in deadlock detection: a host is not a task, so a call here
// never makes a script's wait look deadlocked, and never prevents it from being
// reported.

// Task is a script's task seen from the host. Every method is safe to call
// from any goroutine.
type Task interface {
	// Value is the task as a Scheme value, to hand back to a script.
	Value() Value
	// Done is closed when the task has finished, by value, error or
	// cancellation. Select on it to be told instead of polling.
	Done() <-chan struct{}
	// Result is the task's value or error once Done is closed, and
	// ErrTaskRunning before. A task that was cancelled returns an error for
	// which errors.Is(err, context.Canceled) holds.
	Result() (Value, error)
	// Cancel cancels the task's context, as task-cancel! does. It does
	// nothing to a finished task.
	Cancel()
}

// ErrTaskRunning is what Task.Result returns for a task that has not finished.
var ErrTaskRunning = errors.New("the task has not finished")

// AsTask returns the task v holds, if v is one.
func AsTask(v Value) (Task, bool) {
	t, ok := ObjectOf(v).(*taskObj)
	if !ok {
		return nil, false
	}
	return t, true
}

func (t *taskObj) Value() Value          { return ObjectValue(t) }
func (t *taskObj) Done() <-chan struct{} { return t.done }

func (t *taskObj) Result() (Value, error) {
	rt := t.root.rt
	rt.mu.Lock()
	defer rt.mu.Unlock()
	switch {
	case !t.isDone.Load():
		return Value{}, ErrTaskRunning
	case t.cancelled:
		cause := t.cause
		if cause == nil {
			cause = context.Canceled
		}
		return Value{}, fmt.Errorf("task %d was cancelled: %w", t.id, cause)
	}
	return t.val, t.err
}

func (t *taskObj) Cancel() {
	if t.cancel != nil {
		t.cancel(context.Canceled)
	}
}

// endMain marks the task that started an evaluation as over with it.
func (t *taskObj) endMain() {
	rt := t.root.rt
	rt.mu.Lock()
	t.err, t.cancelled, t.cause = errRootDone, true, errRootDone
	t.isDone.Store(true)
	rt.mu.Unlock()
	close(t.done)
}

// Channel is a script's channel seen from the host. Every method is safe to
// call from any goroutine, and none of them waits for a script (only Recv
// waits, for the channel, and stops with its context).
//
// A host that sends to a channel a script may wait on should mark it HostFed:
// otherwise the script's wait is reported as a deadlock when nothing else is
// running.
type Channel interface {
	// Value is the channel as a Scheme value, to hand to a script.
	Value() Value
	// TrySend sends v if that needs no waiting: a receiver is parked, or the
	// buffer has room. It reports whether it did. A closed channel is an
	// error. Give a channel the host sends to some capacity, since a
	// rendezvous channel with no receiver waiting accepts nothing.
	TrySend(v Value) (sent bool, err error)
	// TryRecv receives a value if one is ready (ok), or reports that the
	// channel is closed and drained (closed). With neither, nothing is ready.
	TryRecv() (v Value, ok, closed bool)
	// Recv waits for a value. ok is false when the channel is closed and
	// drained; err is ctx's if it ended first.
	Recv(ctx context.Context) (v Value, ok bool, err error)
	// Close closes the channel, waking everything waiting on it. Closing a
	// closed channel does nothing.
	Close()
	// Closed reports whether the channel has been closed. Values it still
	// holds can be received.
	Closed() bool
	// SetHostFed marks the channel as one the host sends to from outside (or
	// not). It affects tasks that wait on it afterwards.
	SetHostFed(bool)
}

// ChannelOption configures NewChannel.
type ChannelOption func(*channelObj)

// HostFed marks the channel as fed from outside the interpreter: a task
// waiting to receive from it is not counted among the tasks that are waiting
// for one another, so it is not reported as deadlocked while the host may
// still send. Without it, a script that waits on the channel with nothing else
// running gets "deadlock: all tasks are waiting for one another and none can
// run".
func HostFed() ChannelOption { return func(ch *channelObj) { ch.hostFed = true } }

// NewChannel makes a channel in in with room for capacity values (0 for a
// rendezvous). The interpreter must have tasks (WithTasks).
func NewChannel(in *Interp, capacity int, opts ...ChannelOption) (Channel, error) {
	if in == nil || in.tasks == nil {
		return nil, errors.New("scheme: NewChannel: the interpreter has no tasks (scheme.WithTasks)")
	}
	if capacity < 0 || capacity > 1<<24 {
		return nil, fmt.Errorf("scheme: NewChannel: capacity %d: expected 0 to %d", capacity, 1<<24)
	}
	ch := in.tasks.newChannel(capacity)
	for _, o := range opts {
		o(ch)
	}
	return ch, nil
}

// AsChannel returns the channel v holds, if v is one.
func AsChannel(v Value) (Channel, bool) {
	ch, ok := ObjectOf(v).(*channelObj)
	if !ok {
		return nil, false
	}
	return ch, true
}

func (ch *channelObj) Value() Value { return ObjectValue(ch) }

func (ch *channelObj) TrySend(v Value) (bool, error) {
	ch.rt.mu.Lock()
	defer ch.rt.mu.Unlock()
	done, err := ch.trySend(v)
	if err != nil {
		return false, err
	}
	return done, nil
}

func (ch *channelObj) TryRecv() (v Value, ok, closed bool) {
	ch.rt.mu.Lock()
	defer ch.rt.mu.Unlock()
	done, v, ok := ch.tryRecv()
	switch {
	case !done:
		return Value{}, false, false
	case ok:
		return v, true, false
	}
	return Value{}, false, true
}

func (ch *channelObj) Recv(ctx context.Context) (Value, bool, error) {
	rt := ch.rt
	rt.mu.Lock()
	if done, v, ok := ch.tryRecv(); done {
		rt.mu.Unlock()
		return v, ok, nil
	}
	// A waiter with no task is not counted, so a host waiting here is not
	// one of the tasks waiting on one another.
	w := newWaiter(nil)
	wc := &wcase{w: w, ch: ch}
	w.cases = append(w.cases, wc)
	ch.recvq = append(ch.recvq, wc)
	rt.park(ctx, w)
	if w.err != nil {
		return Value{}, false, w.err
	}
	return w.val, w.ok, nil
}

func (ch *channelObj) Close() {
	ch.rt.mu.Lock()
	defer ch.rt.mu.Unlock()
	_ = ch.close()
}

func (ch *channelObj) Closed() bool {
	ch.rt.mu.Lock()
	defer ch.rt.mu.Unlock()
	return ch.closed
}

func (ch *channelObj) SetHostFed(b bool) {
	ch.rt.mu.Lock()
	defer ch.rt.mu.Unlock()
	ch.hostFed = b
}

// errShutdown is the cause Shutdown cancels detached tasks with. It is a
// cancellation, so a task that ends with it did not fail.
var errShutdown = fmt.Errorf("the interpreter is shutting down: %w", context.Canceled)

// spawnDetached starts thunk as a task of the interpreter. Its context keeps
// the values of the caller's (the origin, and whatever the host put there) but
// not its cancellation, which would end it with the evaluation. It is its own
// evaluation: tasks it spawns end with it.
func (in *Interp) spawnDetached(c *Call, thunk Value) (Value, error) {
	rt := in.tasks
	if rt.live.Add(1) > int64(rt.limits.MaxTasks) {
		rt.live.Add(-1)
		return Value{}, errorf(c.Name, "too many tasks: the limit is %d alive at once", rt.limits.MaxTasks)
	}
	dctx, cancel := context.WithCancelCause(context.WithoutCancel(c.Ctx))
	rctx, rcancel := context.WithCancelCause(dctx)
	r := &root{rt: rt, cancel: rcancel}
	obj := &taskObj{id: rt.ids.Add(1), root: r, cancel: cancel, done: make(chan struct{}), detached: true}
	t := &task{limit: rt.limits.MaxDepth, root: r, obj: obj}
	rctx = context.WithValue(rctx, taskKey{in}, t)

	rt.mu.Lock()
	if rt.shutdown {
		rt.mu.Unlock()
		rt.live.Add(-1)
		cancel(nil)
		return Value{}, errorf(c.Name, "the interpreter is shutting down")
	}
	rt.alive++
	rt.detached[obj] = struct{}{}
	rt.bgWG.Add(1)
	rt.mu.Unlock()
	go in.runDetached(rctx, t, obj, thunk)
	return ObjectValue(obj), nil
}

func (in *Interp) runDetached(ctx context.Context, t *task, obj *taskObj, thunk Value) {
	rt, r := in.tasks, obj.root
	var v Value
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				v, err = Value{}, errorf("task", "task %d panicked: %v", obj.id, p)
			}
		}()
		v, err = in.apply(t, ctx, thunk, nil)
	}()
	cancelled := err != nil && ctx.Err() != nil
	cause := context.Cause(ctx)
	// Its own tasks end with it, as an evaluation's do. A denial or halt in
	// one of them is this task's error.
	err = r.end(err, false)
	cancelled = cancelled && !isFatal(err)

	rt.mu.Lock()
	obj.val, obj.err, obj.cancelled = v, err, cancelled
	if cancelled {
		obj.cause = cause
	}
	obj.isDone.Store(true)
	heard := obj.observed.Load() || len(obj.joiners) > 0
	for _, w := range obj.joiners {
		if !w.done {
			rt.fire(w, 0, Value{}, true, nil)
		}
	}
	obj.joiners = nil
	rt.alive--
	delete(rt.detached, obj)
	rt.checkDeadlock()
	rt.mu.Unlock()
	close(obj.done)
	obj.cancel(nil)
	rt.live.Add(-1)
	if err != nil && !cancelled && !heard && in.taskFailed != nil {
		in.taskFailed(obj, err)
	}
	rt.bgWG.Done()
}

// afterDetached is after with a timer the interpreter owns. The channel is
// host-fed until it fires, since the timer will wake whoever waits on it, and
// an ordinary channel afterwards.
func (rt *taskRuntime) afterDetached(d time.Duration) (*channelObj, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.shutdown {
		return nil, errors.New("the interpreter is shutting down")
	}
	ch := rt.newChannel(1)
	ch.hostFed = true
	p := &pendingTimer{}
	rt.bgTimers[p] = ch
	p.timer = time.AfterFunc(d, func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if p.fired {
			return
		}
		p.fired = true
		delete(rt.bgTimers, p)
		ch.hostFed = false
		ch.trySend(True)
	})
	return ch, nil
}

// Shutdown ends everything the interpreter owns: it cancels every detached
// task and timer and waits for the tasks to finish, for as long as ctx
// allows. It returns ctx's error if some are still running then (a handler
// that ignores its context can do that). It is safe to call more than once and
// from any goroutine, and evaluations may go on, but spawn-detached and
// after-detached are refused from the first call. Without TaskLimits.Background
// it does nothing.
func (in *Interp) Shutdown(ctx context.Context) error {
	rt := in.tasks
	if rt == nil {
		return nil
	}
	rt.mu.Lock()
	rt.shutdown = true
	for obj := range rt.detached {
		obj.cancel(errShutdown)
	}
	for p, ch := range rt.bgTimers {
		p.fired = true
		p.timer.Stop()
		delete(rt.bgTimers, p)
		ch.hostFed = false
		_ = ch.close()
	}
	rt.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		rt.bgWG.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		rt.mu.Lock()
		n := len(rt.detached)
		rt.mu.Unlock()
		return fmt.Errorf("shutdown: %d detached task(s) still running: %w", n, ctx.Err())
	}
}
