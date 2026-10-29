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
		in.tasks = &taskRuntime{limits: l}
	}
}

// taskRuntime is an interpreter's tasks.
type taskRuntime struct {
	limits TaskLimits
	live   atomic.Int64 // spawned tasks alive now
	ids    atomic.Int64
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
}

func (rt *taskRuntime) newRoot(ctx context.Context) (context.Context, *root) {
	ctx, cancel := context.WithCancelCause(ctx)
	return ctx, &root{rt: rt, cancel: cancel}
}

// leave ends the evaluation: it cancels what is still running, waits for it,
// and returns the error the evaluation ends with. A denial or halt in any task
// wins, since a script must not hide a refusal by running the refused call in a
// task; then the evaluation's own error; then a task that failed with an error
// no one asked for, since a silent background failure is what an orchestrator
// least wants.
func (r *root) leave(err error) error {
	r.cancel(errRootDone)
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
	done   chan struct{} // closed once val and err are set

	val       Value
	err       error
	cancelled bool        // the task ended because its context was cancelled
	observed  atomic.Bool // someone waited for it
}

func (t *taskObj) Kind() string { return "task" }
func (t *taskObj) String() string {
	if t.id == 0 {
		return "#<task main>"
	}
	return fmt.Sprintf("#<task %d>", t.id)
}

func (t *taskObj) finished() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// unobserved is the error an evaluation returns for a task that failed and
// that no one waited for.
func (t *taskObj) unobserved() error {
	var se *Error
	if errors.As(t.err, &se) {
		return &Error{Message: fmt.Sprintf("task %d failed and no one waited for it: %s", t.id, se.Message), Irritants: se.Irritants, Cause: t.err, Taint: se.Taint}
	}
	return &Error{Message: fmt.Sprintf("task %d failed and no one waited for it: %v", t.id, t.err), Cause: t.err}
}

// spawn starts thunk as a new task of the calling task's evaluation.
func (in *Interp) spawn(c *Call, thunk Value) (Value, error) {
	rt, r := in.tasks, c.t.root
	if rt.live.Add(1) > int64(rt.limits.MaxTasks) {
		rt.live.Add(-1)
		return Value{}, errorf(c.Name, "too many tasks: the limit is %d alive at once", rt.limits.MaxTasks)
	}
	ctx, cancel := context.WithCancelCause(c.Ctx)
	obj := &taskObj{id: rt.ids.Add(1), root: r, cancel: cancel, done: make(chan struct{})}
	t := &task{limit: rt.limits.MaxDepth, root: r, obj: obj}
	ctx = context.WithValue(ctx, taskKey{in}, t)
	r.wg.Add(1)
	go in.runTask(ctx, t, obj, thunk)
	return ObjectValue(obj), nil
}

func (in *Interp) runTask(ctx context.Context, t *task, obj *taskObj, thunk Value) {
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
	v, err = in.apply(t, ctx, thunk, nil)
}

func (t *taskObj) finish(ctx context.Context, v Value, err error) {
	r := t.root
	t.val, t.err = v, err
	if err != nil {
		// A task whose context was cancelled was stopped, not broken: its
		// error is the cancellation's, however a handler spelled it.
		t.cancelled = ctx.Err() != nil
		switch {
		case isFatal(err):
			r.setFatal(err)
		case !t.cancelled:
			r.noteFailure(t)
		}
	}
	close(t.done)
	t.cancel(nil)
	r.rt.live.Add(-1)
	r.wg.Done()
}

// wait blocks until t is finished or ctx is done, and returns t's outcome.
func (t *taskObj) wait(c *Call) (Value, error) {
	select {
	case <-t.done:
	case <-c.Ctx.Done():
		return Value{}, wrap(c.Name, c.Ctx.Err())
	}
	t.observed.Store(true)
	switch {
	case t.err == nil:
		return t.val, nil
	case t.cancelled:
		return Value{}, errorf(c.Name, "task %d was cancelled", t.id)
	}
	return Value{}, t.err
}
