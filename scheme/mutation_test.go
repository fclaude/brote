package scheme

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestSetCarAndSetCdr(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		{`(define p (list 1 2 3)) (set-car! p 'a) p`, `(a 2 3)`},
		{`(define p (list 1 2 3)) (set-cdr! (cdr p) '(x y)) p`, `(1 2 x y)`},
		{`(define p (list 1 2)) (set-cdr! p 9) p`, `(1 . 9)`},
	} {
		if got := evalAll(t, in, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
	// A list built a piece at a time.
	got := evalAll(t, New(), `
(define head (list 0))
(define tail head)
(define (append! x) (let ((cell (list x))) (set-cdr! tail cell) (set! tail cell)))
(append! 1) (append! 2) (append! 3)
head`)
	if got != "(0 1 2 3)" {
		t.Errorf("got %s", got)
	}
	// What was shared shows the change, and what was copied does not.
	got = evalAll(t, New(), `
(define a (list 1 2))
(define b a)
(define c (append a '()))
(set-car! a 'x)
(list b c)`)
	if got != "((x 2) (1 2))" {
		t.Errorf("got %s", got)
	}
}

func TestSetCarKeepsTheTaintOfWhatIsStored(t *testing.T) {
	in := New()
	in.mustRegister("tainted-text", "Text with a provenance.", nil, false, func(c *Call) (Value, error) {
		return Tainted(Str("secret"), TaintOf("test:source")), nil
	})
	v, err := in.EvalString(context.Background(), `(define l (list 1 2)) (set-car! l (tainted-text)) l`)
	if err != nil {
		t.Fatal(err)
	}
	if !DeepTaint(v).Has("test:source") {
		t.Errorf("a list that holds tainted text does not carry the taint: %s", DeepTaint(v))
	}
}

func TestCircularListsAreRefused(t *testing.T) {
	in := New()
	for _, src := range []string{
		`(define l (list 1 2 3)) (set-cdr! (cddr l) l)`,
		`(define l (list 1 2)) (set-cdr! l l)`,
		`(define l (list 1 2)) (set-car! l l)`,
		`(define l (list 1 2 3)) (set-car! (cdr l) (list 'a l))`,
	} {
		_, err := in.EvalString(context.Background(), src)
		if err == nil || !strings.Contains(err.Error(), "circular") {
			t.Errorf("%s: error = %v, want one about a circular structure", src, err)
		}
	}
	// And the list is as it was, so everything that walks it still ends.
	if got := evalAll(t, in, `(list (length l) l)`); !strings.HasPrefix(got, "(3 ") {
		t.Errorf("got %s", got)
	}
}

func TestSetCarErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`(set-car! 5 1)`, "pair"},
		{`(set-cdr! '() 1)`, "pair"},
		{`(set-car! (list 1))`, "argument"},
	} {
		_, err := New().EvalString(context.Background(), c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one that mentions %q", c.src, err, c.want)
		}
	}
}

func TestPairsAreNotChangedWhileTasksAreAlive(t *testing.T) {
	in := New(WithTasks(TaskLimits{}))
	_, err := in.EvalString(context.Background(), `
(define p (list 1 2))
(define ch (make-channel))
(define t (spawn (lambda () (channel-receive ch))))`+"\n"+`(set-car! p 'x)`)
	if err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("error = %v, want one about tasks being alive", err)
	}
	// Once they are done, it is allowed.
	got := evalAll(t, New(WithTasks(TaskLimits{})), `
(define p (list 1 2))
(define t (spawn (lambda () (car p))))
(task-wait t)
(set-car! p 'x)
p`)
	if got != "(x 2)" {
		t.Errorf("got %s", got)
	}
}

func TestPairsAreNotChangedWhileAnotherEvaluationRuns(t *testing.T) {
	in := New(WithTasks(TaskLimits{}))
	started := make(chan struct{})
	release := make(chan struct{})
	in.mustRegister("hold", "Wait for the test.", nil, false, func(c *Call) (Value, error) {
		close(started)
		<-release
		return Void, nil
	})
	if _, err := in.EvalString(context.Background(), `(define p (list 1 2))`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		in.EvalString(context.Background(), `(hold)`)
	}()
	<-started
	_, err := in.EvalString(context.Background(), `(set-car! p 'x)`)
	close(release)
	wg.Wait()
	if err == nil || !strings.Contains(err.Error(), "another evaluation") {
		t.Fatalf("error = %v, want one about another evaluation", err)
	}
	if got := evalAll(t, in, `p`); got != "(1 2)" {
		t.Errorf("p = %s after a refused change", got)
	}
}

func TestChangingAPairForgetsWhatWasKeptAboutItAsCode(t *testing.T) {
	// The form (f 1) is code; once its pair is data that is changed, a later
	// walk over it must see the change.
	got := evalAll(t, New(), `
(define form (list 'list 1 2))
(define (arity l) (length (cdr l)))
(list (arity form) (begin (set-cdr! form '(1)) (arity form)))`)
	if got != "(2 1)" {
		t.Errorf("got %s", got)
	}
}

func TestNestedQuasiquote(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		{"`(1 ,(+ 1 1))", `(1 2)`},
		{"`(1 ,@(list 2 3) 4)", `(1 2 3 4)`},
		{"`(a `(b ,(c ,(+ 1 2))))", "(a (quasiquote (b (unquote (c 3)))))"},
		{"(define x 5) `(a `(b ,,x))", "(a (quasiquote (b (unquote 5))))"},
		{"(define x 5) `(a `(b ,(c ,x)))", "(a (quasiquote (b (unquote (c 5)))))"},
		{"(define l '(1 2)) `(a `(b ,@(c ,@l)))", "(a (quasiquote (b (unquote-splicing (c 1 2)))))"},
		{"`#(1 ,(+ 1 1) ,@(list 3 4))", `#(1 2 3 4)`},
		{"`(1 . ,(+ 1 1))", `(1 . 2)`},
		{"`,(+ 1 2)", `3`},
		{"`(1 `,(+ 1 2))", "(1 (quasiquote (unquote (+ 1 2))))"},
	} {
		if got := evalAll(t, in, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
	// A quasiquote built by another quasiquote can be unquoted in turn.
	got := evalAll(t, in, "(define x 5) (define inner `(+ 1 ,x)) inner")
	if got != "(+ 1 5)" {
		t.Errorf("got %s", got)
	}
	_, err := in.EvalString(context.Background(), "`,@(list 1)")
	if err == nil || !strings.Contains(err.Error(), "unquote-splicing") {
		t.Errorf("error = %v, want one about unquote-splicing", err)
	}
}
