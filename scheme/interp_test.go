package scheme

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWithMaxSteps(t *testing.T) {
	// An infinite tail loop under a budget fails with a step-budget error
	// instead of hanging.
	in := New(WithMaxSteps(1000))
	_, err := in.EvalString(context.Background(), `(let loop () (loop))`)
	if err == nil || !strings.Contains(err.Error(), "step budget exhausted") {
		t.Fatalf("infinite loop under WithMaxSteps(1000): %v", err)
	}

	// A finite program under a generous budget completes with the right value.
	in2 := New(WithMaxSteps(1_000_000))
	v, err := in2.EvalString(context.Background(), `(let loop ((n 0) (acc 0)) (if (= n 10000) acc (loop (+ n 1) (+ acc n))))`)
	if err != nil {
		t.Fatalf("finite loop: %v", err)
	}
	if got := Write(v); got != "49995000" {
		t.Fatalf("finite loop gave %s, want 49995000", got)
	}

	// A small program fits in a small budget.
	in3 := New(WithMaxSteps(10))
	if v, err := in3.EvalString(context.Background(), `(+ 1 2)`); err != nil || Write(v) != "3" {
		t.Fatalf("(+ 1 2) under WithMaxSteps(10): %v %s", err, Write(v))
	}

	// Without the option the behaviour is as before: only the context stops
	// an evaluation.
	in4 := New()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err = in4.EvalString(ctx, `(let loop () (loop))`)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("infinite loop without a budget, cancelled ctx: %v", err)
	}

	// The budget error is an ordinary error in the MaxDepth style, but it
	// fires only when the budget is spent, so no steps are left to handle it
	// with: like a cancelled context, guard cannot catch it in practice.
	in5 := New(WithMaxSteps(100))
	_, err = in5.EvalString(context.Background(), `(let loop () (loop))`)
	if err == nil || !strings.Contains(err.Error(), "step budget exhausted: more than 100 evaluation steps") {
		t.Fatalf("budget error text: %v", err)
	}
}

// The tasks an evaluation starts share its budget: a chain of detached tasks,
// each doing a little work and handing on to the next, would otherwise run
// forever on fresh budgets.
func TestWithMaxStepsTasks(t *testing.T) {
	in := New(WithMaxSteps(20_000), WithTasks(TaskLimits{Background: true}), WithTaskFailed(func(Task, error) {}))
	defer in.Shutdown(context.Background())
	_, err := in.EvalString(context.Background(), `
(define links (make-atom 0))
(define (chain)
  (swap! links (lambda (n) (+ n 1)))
  (let loop ((i 0)) (if (< i 50) (loop (+ i 1)) (spawn-detached chain))))
(spawn-detached chain)`)
	if err != nil {
		t.Fatal(err)
	}
	// Each link takes a few hundred steps, so the budget allows well under
	// a hundred of them; give the chain time to run past that if it could.
	time.Sleep(300 * time.Millisecond)
	first, err := in.EvalString(context.Background(), `(atom-ref links)`)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	second, _ := in.EvalString(context.Background(), `(atom-ref links)`)
	if first.I > 200 || second.I != first.I {
		t.Errorf("the chain ran %d links, then %d: it escaped the step budget", first.I, second.I)
	}
}
