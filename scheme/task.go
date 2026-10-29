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
	// Tail calls loop inside one eval and do not add to it; MaxDepth bounds it.
	depth int
	// out is where display, write and newline go while with-output-to-string
	// is capturing; nil means the interpreter's writer.
	out io.Writer
	// printed is the taint of what has been printed since the capture began;
	// the string with-output-to-string returns has it.
	printed Taint
}

type taskKey struct{}

// enter returns the task ctx belongs to and a context that carries it. A
// context that carries none starts a new task, so a host call from outside
// any evaluation is its own task; a handler that passes its Call.Ctx on
// (as map and register-tool do) stays in its caller's.
func enter(ctx context.Context) (*task, context.Context) {
	if t, ok := ctx.Value(taskKey{}).(*task); ok {
		return t, ctx
	}
	t := new(task)
	return t, context.WithValue(ctx, taskKey{}, t)
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
