package scheme_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

// taskInterp is an interpreter with tasks and a few primitives to test them
// with: sleep-ms waits (and stops with its context), hold waits for its
// context and counts how many are waiting, boom fails, crash panics, who names
// the origin of the call, dirty returns a tainted string, forbidden is
// refused by the gate.
type taskEnv struct {
	in      *scheme.Interp
	holding atomic.Int64
	out     *strings.Builder
}

func newTaskInterp(t *testing.T, limits scheme.TaskLimits, gate scheme.Gate) *taskEnv {
	t.Helper()
	e := &taskEnv{out: new(strings.Builder)}
	e.in = scheme.New(scheme.WithTasks(limits), scheme.WithOutput(e.out))
	reg := func(name string, params []scheme.Param, meta scheme.Meta, h func(c *scheme.Call) (scheme.Value, error)) {
		meta.Doc, meta.Params = name+" (test)", params
		if err := e.in.Register(&scheme.Prim{Name: name, Meta: meta, Handler: h}); err != nil {
			t.Fatal(err)
		}
	}
	reg("sleep-ms", []scheme.Param{{Name: "ms", Type: "integer"}}, scheme.Meta{}, func(c *scheme.Call) (scheme.Value, error) {
		ms, err := c.Int(0)
		if err != nil {
			return scheme.Value{}, err
		}
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return scheme.Void, nil
		case <-c.Ctx.Done():
			return scheme.Value{}, c.Ctx.Err()
		}
	})
	reg("hold", nil, scheme.Meta{}, func(c *scheme.Call) (scheme.Value, error) {
		e.holding.Add(1)
		defer e.holding.Add(-1)
		<-c.Ctx.Done()
		return scheme.Value{}, c.Ctx.Err()
	})
	reg("boom", nil, scheme.Meta{}, func(c *scheme.Call) (scheme.Value, error) {
		return scheme.Value{}, errors.New("it broke")
	})
	reg("crash", nil, scheme.Meta{}, func(c *scheme.Call) (scheme.Value, error) { panic("handler bug") })
	reg("who", nil, scheme.Meta{}, func(c *scheme.Call) (scheme.Value, error) {
		return scheme.Str(string(c.Origin)), nil
	})
	reg("dirty", nil, scheme.Meta{TaintSource: scheme.TagFile}, func(c *scheme.Call) (scheme.Value, error) {
		return scheme.Str("data"), nil
	})
	reg("forbidden", nil, scheme.Meta{}, func(c *scheme.Call) (scheme.Value, error) {
		return scheme.Str("reached"), nil
	})
	e.in.SetGate(gate)
	return e
}

func (e *taskEnv) eval(src string) (scheme.Value, error) {
	return e.in.EvalString(context.Background(), src)
}

func (e *taskEnv) ok(t *testing.T, src string) scheme.Value {
	t.Helper()
	v, err := e.eval(src)
	if err != nil {
		t.Fatalf("%s\n  error: %v", src, err)
	}
	return v
}

func (e *taskEnv) fails(t *testing.T, src, contains string) error {
	t.Helper()
	_, err := e.eval(src)
	if err == nil {
		t.Fatalf("%s\n  no error, want one containing %q", src, contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("%s\n  error %q, want one containing %q", src, err, contains)
	}
	return err
}

func defaultLimits() scheme.TaskLimits { return scheme.TaskLimits{} }

func TestSpawnAndWait(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	if got := scheme.Write(e.ok(t, `(task-wait (spawn (lambda () (+ 1 2))))`)); got != "3" {
		t.Errorf("got %s", got)
	}
	if got := scheme.Write(e.ok(t, `(let ((t (spawn (lambda () 7)))) (list (task? t) (task-wait t) (task-done? t)))`)); got != "(#t 7 #t)" {
		t.Errorf("got %s", got)
	}
	if got := scheme.Write(e.ok(t, `(task-wait-all (map (lambda (n) (spawn (lambda () (* n n)))) '(1 2 3 4)))`)); got != "(1 4 9 16)" {
		t.Errorf("got %s", got)
	}
	// (the task may start before define has bound t, so it waits for it)
	if got := scheme.Write(e.ok(t, `(define t (spawn (lambda () (let loop () (if (task? t) (eq? (current-task) t) (loop))))))
(task-wait t)`)); got != "#t" {
		t.Errorf("a task's current-task is not the handle spawn returned: %s", got)
	}
}

func TestTasksAreNotDefinedWithoutTheGrant(t *testing.T) {
	in := scheme.New()
	_, err := in.EvalString(context.Background(), `(spawn (lambda () 1))`)
	if err == nil || !strings.Contains(err.Error(), "unbound variable: spawn") {
		t.Errorf("got %v", err)
	}
}

func TestTasksRunAtTheSameTime(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	start := time.Now()
	e.ok(t, `(task-wait-all (map (lambda (i) (spawn (lambda () (sleep-ms 200) i))) (iota 8)))`)
	if d := time.Since(start); d > 700*time.Millisecond {
		t.Errorf("eight 200ms sleeps took %v: they did not overlap", d)
	}
}

func TestTasksShareData(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.ok(t, `(define h (make-hash))
(define v (vector 0 0 0 0))
(task-wait-all (map (lambda (i)
  (spawn (lambda ()
    (let loop ((n 0))
      (when (< n 200)
        (hash-update! h "n" (lambda (x) (+ x 1)) 0)
        (vector-set! v (modulo i 4) n)
        (loop (+ n 1)))))))
  (iota 16)))`)
	if got := scheme.Write(e.ok(t, `(hash-ref h "n")`)); got != "3200" {
		t.Errorf("hash-update! lost updates: %s", got)
	}
}

func TestATaskErrorReachesTheWaiter(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.fails(t, `(task-wait (spawn (lambda () (error "bad thing" 42))))`, "bad thing")
	if got := scheme.Write(e.ok(t, `(guard (e (#t (cdr (assq 'message e)))) (task-wait (spawn (lambda () (boom)))))`)); !strings.Contains(got, "it broke") {
		t.Errorf("guard did not see the task's error: %s", got)
	}
}

func TestAPanicInAHandlerIsTheTasksFailure(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.fails(t, `(task-wait (spawn (lambda () (crash))))`, "panicked")
	// and the interpreter is fine afterwards
	e.ok(t, `(+ 1 1)`)
}

func TestAFailedTaskNoOneWaitedForFailsTheEvaluation(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.fails(t, `(spawn (lambda () (error "lost")))
(sleep-ms 50)
'fine`, "no one waited for it: lost")
	// one that was waited for, even with the error caught, is not unobserved
	e.ok(t, `(spawn (lambda () 1))
(guard (e (#t 'caught)) (task-wait (spawn (lambda () (error "seen")))))`)
}

func TestCancel(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	start := time.Now()
	got := scheme.Write(e.ok(t, `
(define t (spawn (lambda () (sleep-ms 10000) 'finished)))
(task-cancel! t)
(guard (e (#t (cdr (assq 'message e)))) (task-wait t))`))
	if !strings.Contains(got, "cancelled") {
		t.Errorf("got %s", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("cancel did not stop the sleep")
	}
	// A cancelled task cannot catch its own cancellation.
	e.ok(t, `
(define t (spawn (lambda () (guard (e (#t 'caught)) (hold)) 'went-on)))
(task-cancel! t)
(guard (e (#t 'cancelled)) (task-wait t))`)
	// Cancelling a task that is a spinning loop.
	e.ok(t, `
(define t (spawn (lambda () (let loop () (loop)))))
(task-cancel! t)
(guard (e (#t 'cancelled)) (task-wait t))`)
	// and a cancelled task nobody waits for is not a failure
	e.ok(t, `(task-cancel! (spawn (lambda () (hold))))`)
}

func TestTheHostsContextStopsEveryTask(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := e.in.EvalString(ctx, `(task-wait-all (list (spawn (lambda () (hold))) (spawn (lambda () (let loop () (loop))))))`)
	if err == nil {
		t.Fatal("no error")
	}
	if time.Since(start) > 3*time.Second || e.holding.Load() != 0 {
		t.Errorf("tasks outlived their context (%v, %d holding)", time.Since(start), e.holding.Load())
	}
}

// Nothing a call started runs after it returns.
func TestTasksEndWithTheEvaluationThatStartedThem(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.ok(t, `(spawn (lambda () (hold))) (spawn (lambda () (hold))) (sleep-ms 50) 'done`)
	if n := e.holding.Load(); n != 0 {
		t.Errorf("%d tasks still running after the evaluation returned", n)
	}
	// including when the evaluation fails
	e.fails(t, `(spawn (lambda () (hold))) (sleep-ms 50) (error "root failed")`, "root failed")
	if n := e.holding.Load(); n != 0 {
		t.Errorf("%d tasks still running after the evaluation failed", n)
	}
}

func TestTheTaskLimit(t *testing.T) {
	e := newTaskInterp(t, scheme.TaskLimits{MaxTasks: 3}, nil)
	e.fails(t, `(define ts (map (lambda (i) (spawn (lambda () (hold)))) (iota 5)))`, "too many tasks")
	// the slots come back when the tasks end
	e.ok(t, `(task-wait-all (map (lambda (i) (spawn (lambda () i))) (iota 3)))`)
	e.ok(t, `(task-wait-all (map (lambda (i) (spawn (lambda () i))) (iota 3)))`)
}

func TestDepthInsideATask(t *testing.T) {
	e := newTaskInterp(t, scheme.TaskLimits{MaxDepth: 100}, nil)
	e.ok(t, `(define (down n) (if (= n 0) 0 (+ 1 (down (- n 1)))))`)
	e.ok(t, `(task-wait (spawn (lambda () (down 50))))`)
	e.fails(t, `(task-wait (spawn (lambda () (down 500))))`, "recursion too deep")
	// the evaluation that started the tasks keeps the full limit
	e.ok(t, `(down 500)`)
}

func TestATaskKeepsItsParentsOrigin(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	for _, o := range []scheme.Origin{scheme.OriginUser, scheme.OriginModel} {
		ctx := scheme.WithOrigin(context.Background(), o)
		v, err := e.in.EvalString(ctx, `(task-wait (spawn (lambda () (who))))`)
		if err != nil || v.S != string(o) {
			t.Errorf("origin %s: got %s, %v", o, scheme.Write(v), err)
		}
	}
}

func TestTaintCrossesTasks(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	v := e.ok(t, `(task-wait (spawn (lambda () (dirty))))`)
	if !v.Taint.Has(scheme.TagFile) {
		t.Errorf("a value lost its provenance through a task: %v", v.Taint)
	}
	v = e.ok(t, `(define x (dirty)) (task-wait (spawn (lambda () (string-append "a" x))))`)
	if !v.Taint.Has(scheme.TagFile) {
		t.Errorf("a captured value lost its provenance: %v", v.Taint)
	}
}

func TestOutputCaptureBelongsToTheTaskThatAskedForIt(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := e.ok(t, `
(define child (spawn (lambda () (sleep-ms 30) (display "from child") 'ok)))
(define captured (with-output-to-string (display "mine") (sleep-ms 100) (display "!")))
(task-wait child)
captured`)
	if got.S != "mine!" {
		t.Errorf("capture = %q", got.S)
	}
	if e.out.String() != "from child" {
		t.Errorf("host output = %q", e.out.String())
	}
}

// --- the guard ---

// refusing denies one primitive, as a policy would.
type refusing struct {
	prim   string
	denied atomic.Int64
}

func (g *refusing) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if p.Name == g.prim {
		g.denied.Add(1)
		return &scheme.Denied{Prim: p.Name, Reason: p.Name + " is not allowed"}
	}
	return nil
}
func (g *refusing) RegisterChecker(name, verdict string, predicate scheme.Value) error { return nil }

func TestADenialInATaskCannotBeCaught(t *testing.T) {
	g := &refusing{prim: "forbidden"}
	e := newTaskInterp(t, defaultLimits(), g)
	err := e.fails(t, `(guard (e (#t 'hidden)) (task-wait (spawn (lambda () (forbidden)))))`, "denied")
	var d *scheme.Denied
	if !errors.As(err, &d) {
		t.Errorf("the evaluation did not end with the denial: %T %v", err, err)
	}
	// Neither inside the task...
	e.fails(t, `(task-wait (spawn (lambda () (guard (e (#t 'hidden)) (forbidden)))))`, "denied")
	// ...nor by never asking: the evaluation still ends with it.
	e.fails(t, `(spawn (lambda () (forbidden))) (sleep-ms 100) 'fine`, "denied")
	// and it stops the other tasks
	e.fails(t, `(spawn (lambda () (hold))) (spawn (lambda () (forbidden))) (sleep-ms 2000) 'fine`, "denied")
	if n := e.holding.Load(); n != 0 {
		t.Errorf("%d tasks outlived a denial", n)
	}
}

func TestAHaltInATaskEndsTheEvaluation(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	if err := e.in.Register(&scheme.Prim{Name: "stop", Meta: scheme.Meta{Doc: "test"}, Handler: func(c *scheme.Call) (scheme.Value, error) {
		return scheme.Value{}, &scheme.Halt{Err: errors.New("budget spent")}
	}}); err != nil {
		t.Fatal(err)
	}
	err := e.fails(t, `(guard (e (#t 'hidden)) (task-wait (spawn (lambda () (stop)))))`, "budget spent")
	var h *scheme.Halt
	if !errors.As(err, &h) {
		t.Errorf("not a halt: %T", err)
	}
}

func TestTheGateCanRefuseSpawnItself(t *testing.T) {
	g := &refusing{prim: "spawn"}
	e := newTaskInterp(t, defaultLimits(), g)
	e.fails(t, `(spawn (lambda () 1))`, "spawn is not allowed")
}

// overlapGate fails if two checks run at once.
type overlapGate struct {
	active  atomic.Int64
	overlap atomic.Bool
	calls   atomic.Int64
}

func (g *overlapGate) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if g.active.Add(1) > 1 {
		g.overlap.Store(true)
	}
	time.Sleep(200 * time.Microsecond)
	g.calls.Add(1)
	g.active.Add(-1)
	return nil
}
func (g *overlapGate) RegisterChecker(name, verdict string, predicate scheme.Value) error { return nil }

type concurrentOverlapGate struct{ overlapGate }

func (g *concurrentOverlapGate) ConcurrentSafe() {}

func TestAGateWrittenForOneGoroutineIsCalledOneCallAtATime(t *testing.T) {
	g := &overlapGate{}
	e := newTaskInterp(t, defaultLimits(), g)
	e.ok(t, `(task-wait-all (map (lambda (i) (spawn (lambda () (let loop ((n 0)) (when (< n 20) (+ n 1) (loop (+ n 1))))))) (iota 16)))`)
	if g.overlap.Load() {
		t.Error("the gate was called concurrently")
	}
	if g.calls.Load() < 16*20 {
		t.Errorf("the gate saw only %d calls", g.calls.Load())
	}
}

func TestAConcurrentGateIsNotSerialised(t *testing.T) {
	g := &concurrentOverlapGate{}
	e := newTaskInterp(t, defaultLimits(), g)
	e.ok(t, `(task-wait-all (map (lambda (i) (spawn (lambda () (let loop ((n 0)) (when (< n 50) (+ n 1) (loop (+ n 1))))))) (iota 16)))`)
	if !g.overlap.Load() {
		t.Skip("no two checks happened to overlap")
	}
}

func TestTasksFromSeveralEvaluationsAtOnce(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	done := make(chan error, 8)
	for g := 0; g < 8; g++ {
		go func() {
			_, err := e.eval(fmt.Sprintf(`(task-wait-all (map (lambda (i) (spawn (lambda () (+ i %d)))) (iota 10)))`, g))
			done <- err
		}()
	}
	for g := 0; g < 8; g++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}
