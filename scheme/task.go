package scheme

import (
	"context"
	"io"
	"sync"
)

// task is one thread of evaluation and the state that belongs to it rather
// than to the interpreter: how deeply it has nested, where its output goes,
// and the provenance of what it has printed.
//
// The evaluator takes the task as an argument, so reading this state costs
// nothing on the hot path, and nothing here is shared between tasks. Every
// evaluation entry (Eval, Apply, EvalString, LoadFile) runs in the task its
// context carries, or in a new one.
type task struct {
	// depth counts the evals in progress in this task, nested ones included.
	// Tail calls loop inside one eval and do not add to it; limit bounds it.
	depth int
	limit int
	// steps counts the iterations of the eval loop in this task, when the
	// interpreter has a step budget (WithMaxSteps).
	steps int64
	// out is where display, write and newline go while with-output-to-string
	// is capturing; nil means the interpreter's writer.
	out io.Writer
	// printed is the taint of what has been printed since the capture began;
	// the string with-output-to-string returns has it.
	printed Taint

	// root is the evaluation this task belongs to, and obj its handle; both
	// are nil unless the interpreter has tasks (WithTasks).
	root *root
	obj  *taskObj
}

// isRoot reports whether t is the task that started its evaluation.
func (t *task) isRoot() bool { return t.obj != nil && t.obj.id == 0 }

// taskKey is the context key under which an interpreter keeps the task a
// context belongs to. It is per interpreter, so a handler that evaluates in
// a second interpreter with its own context does not borrow the first's task.
type taskKey struct{ in *Interp }

// DetachedContext returns a context that keeps ctx's values but carries no
// task of this interpreter, so an evaluation started from another goroutine
// with it — Apply, EvalString — begins a new root of its own, like a detached
// task, while still seeing the values the host put in ctx (the origin, the
// calling module's policy). ctx's cancellation does not propagate: the work
// outlives the call that started it.
func (in *Interp) DetachedContext(ctx context.Context) context.Context {
	// A nil under the task key reads as "no task here": enter starts a new one.
	return context.WithValue(context.WithoutCancel(ctx), taskKey{in}, nil)
}

// enter returns the task ctx belongs to and a context that carries it. A
// context that carries none starts a new task (the root of a new evaluation),
// so a host call from outside any evaluation is its own; a handler that passes
// its Call.Ctx on (as map and register-tool do) stays in its caller's.
//
// When this call started the evaluation, done is non-nil and must be called
// with the evaluation's error once it is over: it cancels the tasks still
// running, waits for them, and returns the error the evaluation ends with.
func (in *Interp) enter(ctx context.Context) (t *task, _ context.Context, done func(error) error) {
	if t, ok := ctx.Value(taskKey{in}).(*task); ok {
		return t, ctx, nil
	}
	t = &task{limit: MaxDepth}
	in.quiesce.Lock()
	in.running++
	in.quiesce.Unlock()
	var r *root
	if in.tasks != nil {
		ctx, r = in.tasks.newRoot(ctx)
		t.root, t.obj = r, &taskObj{id: 0, root: r, done: make(chan struct{})}
	}
	done = func(err error) error {
		if r != nil {
			err = r.leave(err)
			t.obj.endMain()
		}
		in.quiesce.Lock()
		in.running--
		in.quiesce.Unlock()
		return err
	}
	return t, context.WithValue(ctx, taskKey{in}, t), done
}

// writer is where t's output goes: its capture, else the interpreter's.
func (t *task) writer(in *Interp) io.Writer {
	if t.out != nil {
		return t.out
	}
	return in.out
}

// syncWriter serialises writes to the interpreter's writer, which the host
// supplied without knowing that tasks might share it. Each display, write
// and newline reaches it whole, never mixed into another's.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (t *task) leave() { t.depth-- }
