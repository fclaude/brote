package scheme

import "testing"

func TestDelayForce(t *testing.T) {
	in := New(WithTasks(TaskLimits{}))

	// (force (delay (+ 1 2))) is 3.
	if got := Write(eval(t, in, `(force (delay (+ 1 2)))`)); got != "3" {
		t.Errorf("(force (delay (+ 1 2))) => %s, want 3", got)
	}

	// The body does not run before the first force, and runs exactly once.
	if got := Write(eval(t, in, `
(define counter (make-atom 0))
(define p (delay (swap! counter (lambda (n) (+ n 1)))))
(atom-ref counter)`)); got != "0" {
		t.Errorf("the body ran before the first force: counter = %s, want 0", got)
	}
	if got := Write(eval(t, in, `(force p)`)); got != "1" {
		t.Errorf("first (force p) => %s, want 1", got)
	}
	if got := Write(eval(t, in, `(force p)`)); got != "1" {
		t.Errorf("second (force p) => %s, want 1 (memoized)", got)
	}
	if got := Write(eval(t, in, `(atom-ref counter)`)); got != "1" {
		t.Errorf("counter after two forces = %s, want 1", got)
	}

	// A promise whose value is another promise unwraps.
	if got := Write(eval(t, in, `(force (delay (delay 5)))`)); got != "5" {
		t.Errorf("(force (delay (delay 5))) => %s, want 5", got)
	}

	// promise?, and force of a value that is not a promise.
	if got := Write(eval(t, in, `(promise? (delay 1))`)); got != "#t" {
		t.Errorf("(promise? (delay 1)) => %s, want #t", got)
	}
	if got := Write(eval(t, in, `(promise? 5)`)); got != "#f" {
		t.Errorf("(promise? 5) => %s, want #f", got)
	}
	if got := Write(eval(t, in, `(force 42)`)); got != "42" {
		t.Errorf("(force 42) => %s, want 42", got)
	}

	// Many tasks forcing the same promise run the body once.
	if got := Write(eval(t, in, `
(define c2 (make-atom 0))
(define p2 (delay (swap! c2 (lambda (n) (+ n 1)))))
(define ts (map (lambda (i) (spawn (lambda () (force p2)))) (iota 8)))
(task-wait-all ts)
(atom-ref c2)`)); got != "1" {
		t.Errorf("eight concurrent forces left counter = %s, want 1", got)
	}

	// A body may force its own promise (R7RS's reentrancy example): the
	// first run to finish sets the value, and later forces return it.
	if got := Write(eval(t, in, `
(define count 0)
(define x 5)
(define p3 (delay (begin (set! count (+ count 1)) (if (> count x) count (force p3)))))
(define first (force p3))
(set! x 10)
(list first (force p3))`)); got != "(6 6)" {
		t.Errorf("reentrant force => %s, want (6 6)", got)
	}
}
