package scheme_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

// These tests state the contract an embedder relies on (docs/embedding.md).
// They use only the exported API, as an embedder does.

func evalOK(t *testing.T, in *scheme.Interp, src string) scheme.Value {
	t.Helper()
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		t.Fatalf("%s\n  error: %v", src, err)
	}
	return v
}

// A runaway program stops when its context is done whether or not it ever
// applies a primitive, so a host that bounds an evaluation by a deadline is
// not at the mercy of what the program calls.
func TestCancellationNeedsNoPrimitive(t *testing.T) {
	for _, src := range []string{
		`(define (f) (f)) (f)`,
		`(let loop () (loop))`,
		`(do ((i 0 i)) (#f))`,
		`(define (g) (g)) (map (lambda (x) (g)) '(1))`,
	} {
		in := scheme.New()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		done := make(chan error, 1)
		go func() { _, err := in.EvalString(ctx, src); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s: want the deadline's error, got %v", src, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: still running 5s after its deadline", src)
		}
		cancel()
	}
}

// An evaluation that was cancelled leaves the interpreter usable: its depth
// count is restored and its bindings are what the program had defined.
func TestInterpSurvivesCancellation(t *testing.T) {
	in := scheme.New()
	evalOK(t, in, `(define kept 41)`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := in.EvalString(ctx, `(let loop () (loop))`); err == nil {
		t.Fatal("the loop ended")
	}
	if got := scheme.Write(evalOK(t, in, `(+ kept 1)`)); got != "42" {
		t.Fatalf("after a cancelled evaluation: %s", got)
	}
}

// Interpreters share no state: each can run in its own goroutine, which is
// how a host runs several (one per session, one per sub-task). Under -race a
// shared package-level variable would fail here.
func TestInterpsAreIndependent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := scheme.New()
			for j := 0; j < 50; j++ {
				if got := scheme.Write(evalOK(t, in, `(define (f n) (if (= n 0) 0 (+ n (f (- n 1))))) (f 20)`)); got != "210" {
					t.Errorf("got %s", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A handler may call back into the interpreter on its own goroutine, with the
// context it was given: that is how register-tool, map and sort run Scheme
// procedures from Go, and how a host primitive takes a callback.
func TestHandlerCallsBackIntoScheme(t *testing.T) {
	in := scheme.New()
	err := in.Register(&scheme.Prim{
		Name: "twice",
		Meta: scheme.Meta{Doc: "Apply a procedure to x, then to the result.", Params: []scheme.Param{{Name: "f", Type: "any"}, {Name: "x", Type: "any"}}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			f, err := c.Proc(0)
			if err != nil {
				return scheme.Value{}, err
			}
			once, err := c.In.Apply(c.Ctx, f, []scheme.Value{c.Args[1]})
			if err != nil {
				return scheme.Value{}, err
			}
			return c.In.Apply(c.Ctx, f, []scheme.Value{once})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scheme.Write(evalOK(t, in, `(twice (lambda (n) (* n 3)) 2)`)); got != "18" {
		t.Fatalf("got %s", got)
	}
}

// An error a handler returns reaches the program as an error guard can catch,
// and reaches the host, from an uncaught one, as a *scheme.Error whose Cause is
// the handler's own error.
func TestHandlerErrorsReachProgramAndHost(t *testing.T) {
	boom := errors.New("disk on fire")
	in := scheme.New()
	if err := in.Register(&scheme.Prim{
		Name:    "fail",
		Meta:    scheme.Meta{Doc: "Always fails."},
		Handler: func(c *scheme.Call) (scheme.Value, error) { return scheme.Value{}, boom },
	}); err != nil {
		t.Fatal(err)
	}
	if got := scheme.Write(evalOK(t, in, `(guard (e (#t (assoc-ref e 'message))) (fail))`)); got != `"fail: disk on fire"` {
		t.Fatalf("the program saw %s", got)
	}
	_, err := in.EvalString(context.Background(), `(fail)`)
	var se *scheme.Error
	if !errors.As(err, &se) || !errors.Is(err, boom) {
		t.Fatalf("the host saw %T %v", err, err)
	}
}

// A halt is how a host primitive ends the work: guard does not catch it, and
// the host gets the error it wrapped.
func TestHaltPassesThroughGuard(t *testing.T) {
	stop := errors.New("budget spent")
	in := scheme.New()
	if err := in.Register(&scheme.Prim{
		Name:    "stop-now",
		Meta:    scheme.Meta{Doc: "Ends the work."},
		Handler: func(c *scheme.Call) (scheme.Value, error) { return scheme.Value{}, &scheme.Halt{Err: stop} },
	}); err != nil {
		t.Fatal(err)
	}
	_, err := in.EvalString(context.Background(), `(guard (e (#t 'caught)) (stop-now))`)
	var h *scheme.Halt
	if !errors.As(err, &h) || !errors.Is(err, stop) {
		t.Fatalf("a halt was caught or lost: %T %v", err, err)
	}
}

// The origin of an evaluation is the context's, and a context that says
// nothing is not the user's.
func TestOriginDefaultsToNotTheUser(t *testing.T) {
	if got := scheme.OriginFrom(context.Background()); got == scheme.OriginUser {
		t.Fatalf("an unsaid origin is %q", got)
	}
	if got := scheme.OriginFrom(scheme.WithOrigin(context.Background(), scheme.OriginUser)); got != scheme.OriginUser {
		t.Fatalf("origin = %q", got)
	}
}
