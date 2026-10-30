package scheme

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// TaskLimits bounds the tasks a script may start.
type TaskLimits struct {
	// MaxTasks is how many spawned tasks may be alive at once in the
	// interpreter, across every evaluation. Default 256.
	MaxTasks int
	// MaxDepth is how deeply evaluation may nest inside a spawned task,
	// where MaxDepth (the package constant) bounds the task that started the
	// evaluation. A task is a goroutine with its own stack, so this and
	// MaxTasks together bound the memory tasks can use. Default 2500.
	MaxDepth int
	// Background also gives scripts (spawn-detached thunk) and (after-detached
	// secs): tasks and timers owned by the interpreter, which outlive the
	// evaluation that made them and end with Interp.Shutdown. They count
	// against MaxTasks like any task. Off by default: a host that grants it
	// must call Shutdown when it is done with the interpreter.
	Background bool
}

// WithTasks gives scripts tasks: spawn, task-wait and the rest (docs/scheme.md
// §Tasks). It is the host's statement that its handlers and its Gate are safe
// to call from several goroutines at once; a Gate that is not is called one
// call at a time unless it implements ConcurrentGate. Without it the
// procedures are not defined.
func WithTasks(l TaskLimits) Option {
	return func(in *Interp) {
		if l.MaxTasks <= 0 {
			l.MaxTasks = 256
		}
		if l.MaxDepth <= 0 {
			l.MaxDepth = 2500
		}
		in.tasks = &taskRuntime{limits: l, parked: map[*waiter]struct{}{}, detached: map[*taskObj]struct{}{}, bgTimers: map[*pendingTimer]*channelObj{}}
	}
}

// WithTaskFailed registers fn to hear of a detached task (spawn-detached) that
// failed with an error no one was waiting to receive. Such a failure does not
// fail any evaluation, since the evaluation that started the task is long gone
// and a later one is unrelated; this is where it goes instead. A task that
// was cancelled, by task-cancel! or by Shutdown, did not fail and is not
// reported.
//
// fn is called on the failed task's own goroutine, after it has finished and
// outside every lock of the interpreter: it may call into the interpreter but
// must not wait for a long time, and it must not call Shutdown (which waits
// for it). Task is the failed task; its Result is the error.
func WithTaskFailed(fn func(Task, error)) Option {
	return func(in *Interp) { in.taskFailed = fn }
}

// taskRuntime is an interpreter's tasks.
type taskRuntime struct {
	limits TaskLimits
	live   atomic.Int64 // spawned tasks alive now
	ids    atomic.Int64

	// mu guards everything tasks wait on (sync.go): the counts below, every
	// channel, and each task's list of joiners.
	mu      sync.Mutex
	alive   int // tasks that exist: each evaluation's first and every spawned one
	blocked int // of those, the ones parked waiting for another
	timers  int // timers that will wake someone
	parked  map[*waiter]struct{}

	// Detached tasks and timers (TaskLimits.Background), under mu.
	detached map[*taskObj]struct{}
	bgTimers map[*pendingTimer]*channelObj
	shutdown bool
	bgWG     sync.WaitGroup // detached tasks running
}

// errRootDone is the cause the tasks left running when an evaluation ends are
// cancelled with.
var errRootDone = errors.New("the evaluation that started this task has ended")

// root is one evaluation's tasks: the task that started it and everything
// spawned from there. Nothing in it outlives the call that made it.
type root struct {
	rt     *taskRuntime
	cancel context.CancelCauseFunc
	wg     sync.WaitGroup // spawned tasks still running

	mu       sync.Mutex
	fatal    error      // a denial or a halt in some task: it ends the evaluation
	failures []*taskObj // tasks that failed on their own, in case no one waits for them

	timers []*pendingTimer // under rt.mu
}

func (rt *taskRuntime) newRoot(ctx context.Context) (context.Context, *root) {
	ctx, cancel := context.WithCancelCause(ctx)
	rt.mu.Lock()
	rt.alive++
	rt.mu.Unlock()
	return ctx, &root{rt: rt, cancel: cancel}
}

// leave ends the evaluation: it cancels what is still running, waits for it,
// and returns the error the evaluation ends with. A denial or halt in any task
// wins, since a script must not hide a refusal by running the refused call in a
// task; then the evaluation's own error; then a task that failed with an error
// no one asked for, since a silent background failure is what an orchestrator
// least wants.
func (r *root) leave(err error) error { return r.end(err, true) }

// end is leave for a root that counts its first task in alive (an evaluation)
// or does not (a detached task, which the runtime counts until it has published
// its result, so a task waiting for it cannot look deadlocked in between).
func (r *root) end(err error, evaluation bool) error {
	r.cancel(errRootDone)
	r.rt.mu.Lock()
	if evaluation {
		r.rt.alive--
	}
	r.stopTimers()
	r.rt.checkDeadlock()
	r.rt.mu.Unlock()
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fatal != nil {
		return r.fatal
	}
	if err != nil {
		return err
	}
	for _, t := range r.failures {
		if !t.observed.Load() {
			return t.unobserved()
		}
	}
	return nil
}

func (r *root) setFatal(err error) {
	r.mu.Lock()
	if r.fatal == nil {
		r.fatal = err
	}
	r.mu.Unlock()
	r.cancel(err)
}

func (r *root) noteFailure(t *taskObj) {
	r.mu.Lock()
	r.failures = append(r.failures, t)
	r.mu.Unlock()
}

// isFatal reports whether err is one a script can never catch or outlive: the
// guard's refusal and the harness's stop. (A cancelled context is the third
// of Catchable's cases; it ends the task whose context it is.)
func isFatal(err error) bool {
	var d *Denied
	var h *Halt
	return errors.As(err, &d) || errors.As(err, &h)
}

// taskObj is a task as Scheme sees it.
type taskObj struct {
	id   int64
	root *root

	cancel context.CancelCauseFunc

	// val, err and cancelled are set, under rt.mu, before isDone is.
	val       Value
	err       error
	cancelled bool  // the task ended because its context was cancelled
	cause     error // and why, when it was
	isDone    atomic.Bool
	done      chan struct{} // closed once isDone is set
	detached  bool          // spawn-detached: it belongs to the interpreter, not an evaluation
	joiners   []*waiter     // under rt.mu
	observed  atomic.Bool   // someone waited for it
	group     *groupObj     // the group it belongs to, if any
}

func (t *taskObj) Kind() string { return "task" }
func (t *taskObj) String() string {
	if t.id == 0 {
		return "#<task main>"
	}
	return fmt.Sprintf("#<task %d>", t.id)
}

func (t *taskObj) finished() bool { return t.isDone.Load() }

// unobserved is the error an evaluation returns for a task that failed and
// that no one waited for.
func (t *taskObj) unobserved() error {
	var se *Error
	if errors.As(t.err, &se) {
		return &Error{Message: fmt.Sprintf("task %d failed and no one waited for it: %s", t.id, se.Message), Irritants: se.Irritants, Cause: t.err, Taint: se.Taint}
	}
	return &Error{Message: fmt.Sprintf("task %d failed and no one waited for it: %v", t.id, t.err), Cause: t.err}
}

// taskBody is what a task runs.
type taskBody func(ctx context.Context, t *task) (Value, error)

// spawn starts thunk as a new task of the calling task's evaluation.
func (in *Interp) spawn(c *Call, thunk Value) (Value, error) {
	obj, err := in.startTask(c, c.Ctx, nil, func(ctx context.Context, t *task) (Value, error) {
		return in.apply(t, ctx, thunk, nil)
	})
	if err != nil {
		return Value{}, err
	}
	return ObjectValue(obj), nil
}

// startTask runs body as a new task of the calling task's evaluation, under a
// context derived from parent (the caller's, or its group's). A task of a
// group belongs to the group: the group answers for its failure, not the
// evaluation.
func (in *Interp) startTask(c *Call, parent context.Context, g *groupObj, body taskBody) (*taskObj, error) {
	rt, r := in.tasks, c.t.root
	if rt.live.Add(1) > int64(rt.limits.MaxTasks) {
		rt.live.Add(-1)
		return nil, errorf(c.Name, "too many tasks: the limit is %d alive at once", rt.limits.MaxTasks)
	}
	ctx, cancel := context.WithCancelCause(parent)
	obj := &taskObj{id: rt.ids.Add(1), root: r, cancel: cancel, group: g, done: make(chan struct{})}
	t := &task{limit: rt.limits.MaxDepth, root: r, obj: obj, steps: c.t.steps}
	ctx = context.WithValue(ctx, taskKey{in}, t)
	in.quiesce.Lock() // a pair is not changed while a task starts
	rt.mu.Lock()
	rt.alive++
	if g != nil {
		g.pending++
	}
	rt.mu.Unlock()
	in.quiesce.Unlock()
	r.wg.Add(1)
	go runTask(ctx, t, obj, body)
	return obj, nil
}

func runTask(ctx context.Context, t *task, obj *taskObj, body taskBody) {
	var v Value
	var err error
	defer func() {
		// A panic in a handler would end the process from a goroutine the
		// host cannot recover in; make it this task's failure.
		if p := recover(); p != nil {
			v, err = Value{}, errorf("task", "task %d panicked: %v", obj.id, p)
		}
		obj.finish(ctx, v, err)
	}()
	v, err = body(ctx, t)
}

func (t *taskObj) finish(ctx context.Context, v Value, err error) {
	r, rt := t.root, t.root.rt
	cancelled := err != nil && ctx.Err() != nil
	if err != nil {
		// A task whose context was cancelled was stopped, not broken: its
		// error is the cancellation's, however a handler spelled it.
		switch {
		case isFatal(err):
			r.setFatal(err)
		case cancelled:
		case t.group != nil:
			t.observed.Store(true)
			t.group.fail(err)
		default:
			r.noteFailure(t)
		}
	}
	rt.mu.Lock()
	t.val, t.err, t.cancelled = v, err, cancelled
	if cancelled {
		t.cause = context.Cause(ctx)
	}
	t.isDone.Store(true)
	for _, w := range t.joiners {
		if !w.done {
			rt.fire(w, 0, Value{}, true, nil)
		}
	}
	t.joiners = nil
	if g := t.group; g != nil {
		g.pending--
		if g.pending == 0 {
			for _, w := range g.waiters {
				if !w.done {
					rt.fire(w, 0, Value{}, true, nil)
				}
			}
			g.waiters = nil
		}
	}
	rt.alive--
	rt.checkDeadlock()
	rt.mu.Unlock()
	close(t.done)
	t.cancel(nil)
	rt.live.Add(-1)
	r.wg.Done()
}

// wait blocks until t is finished, or ctx is done or nothing can run, and
// returns t's outcome.
func (t *taskObj) wait(c *Call) (Value, error) {
	rt := t.root.rt
	rt.mu.Lock()
	if !t.isDone.Load() {
		w := newWaiter(c.t)
		t.joiners = append(t.joiners, w)
		rt.park(c.Ctx, w)
		rt.mu.Lock()
		for i, x := range t.joiners {
			if x == w {
				t.joiners = append(t.joiners[:i], t.joiners[i+1:]...)
				break
			}
		}
		rt.mu.Unlock()
		if w.err != nil {
			return Value{}, ctxOrDeadlock(c, w.err)
		}
	} else {
		rt.mu.Unlock()
	}
	t.observed.Store(true)
	switch {
	case t.err == nil:
		return t.val, nil
	case t.cancelled && errors.Is(t.cause, errRootDone):
		return Value{}, errorf(c.Name, "task %d was cancelled when the evaluation that started it ended; wait for it in that evaluation (spawn-detached makes a task that outlives it)", t.id)
	case t.cancelled && errors.Is(t.cause, errShutdown):
		return Value{}, errorf(c.Name, "task %d was cancelled when the interpreter shut down", t.id)
	case t.cancelled:
		return Value{}, errorf(c.Name, "task %d was cancelled", t.id)
	}
	return Value{}, t.err
}
