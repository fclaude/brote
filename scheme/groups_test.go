package scheme_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

// concurrency tracks how many calls to (track ms) overlap.
type concurrency struct{ now, max atomic.Int64 }

func (cc *concurrency) install(t *testing.T, e *taskEnv) {
	if err := e.in.Register(&scheme.Prim{
		Name: "track",
		Meta: scheme.Meta{Doc: "track (test)", Params: []scheme.Param{{Name: "ms", Type: "integer"}}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			n := cc.now.Add(1)
			defer cc.now.Add(-1)
			for {
				m := cc.max.Load()
				if n <= m || cc.max.CompareAndSwap(m, n) {
					break
				}
			}
			ms, _ := c.Int(0)
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
				return scheme.Int(ms), nil
			case <-c.Ctx.Done():
				return scheme.Value{}, c.Ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskGroup(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	// waits for every task even if the body does not
	got := scheme.Write(e.ok(t, `
(define done (make-atom 0))
(define r (task-group (lambda (g)
  (group-spawn g (lambda () (sleep-ms 50) (swap! done (lambda (n) (+ n 1)))))
  (group-spawn g (lambda () (sleep-ms 80) (swap! done (lambda (n) (+ n 1)))))
  'body-value)))
(list r (atom-ref done))`))
	if got != "(body-value 2)" {
		t.Errorf("got %s", got)
	}
	// the first failure cancels the others and is the error raised
	start := time.Now()
	err := e.fails(t, `(task-group (lambda (g)
  (group-spawn g (lambda () (hold)))
  (group-spawn g (lambda () (sleep-ms 30) (error "the real failure")))
  (task-wait (group-spawn g (lambda () (hold))))))`, "the real failure")
	_ = err
	if time.Since(start) > 3*time.Second || e.holding.Load() != 0 {
		t.Errorf("siblings were not cancelled (%v, %d holding)", time.Since(start), e.holding.Load())
	}
	// a failing body cancels the group
	e.fails(t, `(task-group (lambda (g) (group-spawn g (lambda () (hold))) (error "body failed")))`, "body failed")
	if e.holding.Load() != 0 {
		t.Error("a task outlived its group")
	}
	// a group that has ended takes no more tasks
	e.fails(t, `(define saved #f) (task-group (lambda (g) (set! saved g))) (group-spawn saved (lambda () 1))`, "has ended")
	// the group's failure is its own: the evaluation does not see it twice
	e.ok(t, `(guard (e (#t 'handled)) (task-group (lambda (g) (group-spawn g (lambda () (error "x"))))))`)
}

func TestPmap(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	if got := scheme.Write(e.ok(t, `(pmap (lambda (x) (* x x)) '(1 2 3 4 5))`)); got != "(1 4 9 16 25)" {
		t.Errorf("got %s", got)
	}
	if got := scheme.Write(e.ok(t, `(pmap (lambda (x) x) '())`)); got != "()" {
		t.Errorf("got %s", got)
	}
	start := time.Now()
	e.ok(t, `(pmap (lambda (i) (sleep-ms 200) i) (iota 10))`)
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Errorf("ten 200ms calls took %v", d)
	}
	// the first failure cancels the others
	start = time.Now()
	e.fails(t, `(pmap (lambda (i) (if (= i 3) (begin (sleep-ms 30) (error "item three")) (hold))) (iota 6))`, "item three")
	if time.Since(start) > 3*time.Second || e.holding.Load() != 0 {
		t.Errorf("pmap did not cancel its other calls (%v, %d holding)", time.Since(start), e.holding.Load())
	}
}

// pmap uses as many tasks as the limit leaves room for rather than failing.
func TestPmapFitsTheTaskLimit(t *testing.T) {
	e := newTaskInterp(t, scheme.TaskLimits{MaxTasks: 4}, nil)
	if got := scheme.Write(e.ok(t, `(length (pmap (lambda (i) (* i 2)) (iota 500)))`)); got != "500" {
		t.Errorf("got %s", got)
	}
	// but a pmap inside a pmap, with no room at all, says so
	e.fails(t, `(pmap (lambda (i) (pmap (lambda (j) (hold)) '(1 2 3 4 5 6))) '(1 2 3 4))`, "too many tasks")
}

func TestPforEachAndSharedAtom(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define total (make-atom 0))
(pfor-each (lambda (i) (swap! total (lambda (n) (+ n i)))) (iota 1000))
(atom-ref total)`))
	if got != "499500" {
		t.Errorf("got %s", got)
	}
}

func TestPmapLimit(t *testing.T) {
	e := newTaskInterp(t, scheme.TaskLimits{MaxTasks: 64}, nil)
	var cc concurrency
	cc.install(t, e)
	got := scheme.Write(e.ok(t, `(pmap-limit 3 (lambda (i) (track 20) i) (iota 24))`))
	if !strings.HasPrefix(got, "(0 1 2 3") || !strings.HasSuffix(got, "23)") {
		t.Errorf("got %s", got)
	}
	if m := cc.max.Load(); m != 3 {
		t.Errorf("at most 3 calls at once should reach 3, saw %d", m)
	}
	e.fails(t, `(pmap-limit 0 (lambda (x) x) '(1))`, "positive")
	// a thousand items with a small limit need only that many tasks
	e.ok(t, `(pmap-limit 4 (lambda (i) i) (iota 1000))`)
}

func TestRace(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	start := time.Now()
	got := scheme.Write(e.ok(t, `(race (list (lambda () (sleep-ms 3000) 'slow) (lambda () (sleep-ms 30) 'fast) (lambda () (hold))))`))
	if got != "fast" || time.Since(start) > 2*time.Second || e.holding.Load() != 0 {
		t.Errorf("got %s in %v (%d holding)", got, time.Since(start), e.holding.Load())
	}
	// one that fails does not end the race
	got = scheme.Write(e.ok(t, `(race (list (lambda () (error "no")) (lambda () (sleep-ms 30) 'survivor)))`))
	if got != "survivor" {
		t.Errorf("got %s", got)
	}
	// (which of two simultaneous failures comes first is not defined)
	if _, err := e.eval(`(race (list (lambda () (error "first")) (lambda () (error "second"))))`); err == nil || !(strings.Contains(err.Error(), "first") || strings.Contains(err.Error(), "second")) {
		t.Errorf("got %v", err)
	}
	e.fails(t, `(race '())`, "no procedures")
}

func TestWithTimeout(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	if got := scheme.Write(e.ok(t, `(with-timeout 5 (lambda () (+ 1 2)))`)); got != "3" {
		t.Errorf("got %s", got)
	}
	start := time.Now()
	e.fails(t, `(with-timeout 0.1 (lambda () (sleep-ms 5000)))`, "timed out")
	if time.Since(start) > 2*time.Second {
		t.Error("the sleep was not cancelled")
	}
	// a busy loop, and a guard inside that tries to catch it
	e.fails(t, `(with-timeout 0.1 (lambda () (guard (e (#t 'swallowed)) (let loop () (loop)))))`, "timed out")
	// the caller can catch it
	if got := scheme.Write(e.ok(t, `(guard (e (#t 'caught)) (with-timeout 0.05 (lambda () (hold))))`)); got != "caught" {
		t.Errorf("got %s", got)
	}
	// waiting for a channel nobody sends to is a timeout, not a deadlock
	e.fails(t, `(with-timeout 0.1 (lambda () (channel-recv (make-channel))))`, "timed out")
	// a timer that did not fire does not keep a later deadlock from being found
	e.ok(t, `(with-timeout 3600 (lambda () 1))`)
	e.fails(t, `(channel-recv (make-channel))`, "deadlock")
	// nested: the inner limit is the shorter
	e.fails(t, `(with-timeout 5 (lambda () (with-timeout 0.05 (lambda () (hold)))))`, "timed out after 50ms")
	// an error that is not the timeout's passes through
	e.fails(t, `(with-timeout 5 (lambda () (error "inner")))`, "inner")
}

func TestMutex(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	// a read-modify-write that loses updates without the lock
	got := scheme.Write(e.ok(t, `
(define m (make-mutex))
(define v (vector 0))
(pfor-each (lambda (i)
  (let loop ((n 0))
    (when (< n 200)
      (with-lock m (lambda () (vector-set! v 0 (+ (vector-ref v 0) 1))))
      (loop (+ n 1)))))
  (iota 16))
(vector-ref v 0)`))
	if got != "3200" {
		t.Errorf("got %s", got)
	}
	// released however the thunk ends
	e.ok(t, `(define m (make-mutex))
(guard (e (#t 'x)) (with-lock m (lambda () (error "inside"))))
(with-lock m (lambda () 'free-again))`)
	// not reentrant: that is a deadlock, and the lock is not left held
	e.fails(t, `(define m (make-mutex)) (with-lock m (lambda () (with-lock m (lambda () 1))))`, "deadlock")
	e.ok(t, `(define m2 (make-mutex)) (with-lock m2 (lambda () 'ok))`)
	// a task waiting for a lock can be cancelled
	e.ok(t, `
(define m (make-mutex))
(define holder (spawn (lambda () (with-lock m (lambda () (hold))))))
(sleep-ms 30)
(define waiter (spawn (lambda () (with-lock m (lambda () 'got-it)))))
(sleep-ms 30)
(task-cancel! waiter)
(guard (e (#t 'cancelled)) (task-wait waiter))
(task-cancel! holder)`)
}

func TestAtoms(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define a (make-atom 10))
(list (atom-ref a) (swap! a (lambda (n) (* n 2))) (begin (atom-set! a 'x) (atom-ref a))
      (compare-and-set! a 'y 'z) (compare-and-set! a 'x 'z) (atom-ref a))`))
	if got != "(10 20 x #f #t z)" {
		t.Errorf("got %s", got)
	}
	// every increment counts, however the tasks interleave
	got = scheme.Write(e.ok(t, `
(define n (make-atom 0))
(pfor-each (lambda (i) (let loop ((k 0)) (when (< k 300) (swap! n (lambda (x) (+ x 1))) (loop (+ k 1))))) (iota 16))
(atom-ref n)`))
	if got != "4800" {
		t.Errorf("got %s", got)
	}
	// a failing function changes nothing
	e.ok(t, `(define a (make-atom 1)) (guard (e (#t 'x)) (swap! a (lambda (n) (error "no"))))`)
	if got := scheme.Write(e.ok(t, `(atom-ref a)`)); got != "1" {
		t.Errorf("got %s", got)
	}
	// taint stays on what an atom holds
	if v := e.ok(t, `(define a (make-atom (dirty))) (atom-ref a)`); !v.Taint.Has(scheme.TagFile) {
		t.Error("an atom dropped the provenance of its value")
	}
}

func TestOnce(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define o (make-once))
(define runs (make-atom 0))
(define results (pmap (lambda (i) (once-do o (lambda () (sleep-ms 50) (swap! runs (lambda (n) (+ n 1))) 'value))) (iota 12)))
(list (atom-ref runs) (car results) (length results))`))
	if got != "(1 value 12)" {
		t.Errorf("got %s", got)
	}
	// a failure is not remembered
	got = scheme.Write(e.ok(t, `
(define o (make-once))
(define tries (make-atom 0))
(guard (e (#t 'failed-once)) (once-do o (lambda () (swap! tries (lambda (n) (+ n 1))) (error "first try"))))
(list (once-do o (lambda () (swap! tries (lambda (n) (+ n 1))) 'second-try)) (atom-ref tries))`))
	if got != "(second-try 2)" {
		t.Errorf("got %s", got)
	}
}

func TestADeadlockInsideAGroupIsFound(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.fails(t, `(task-group (lambda (g) (group-spawn g (lambda () (channel-recv (make-channel))))))`, "deadlock")
	e.fails(t, `(pmap (lambda (i) (channel-recv (make-channel))) '(1 2 3))`, "deadlock")
	// and nothing is left running or held
	e.ok(t, `(+ 1 1)`)
}
