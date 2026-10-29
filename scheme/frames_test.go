package scheme

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestFrameSemantics(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		// the later of two parameters with one name wins, as it always did
		{`((lambda (x x) x) 1 2)`, "2"},
		{`(let ((x 1) (x 2)) x)`, "2"},
		// rest parameters
		{`((lambda args args) 1 2 3)`, "(1 2 3)"},
		{`((lambda (a . rest) (list a rest)) 1 2 3)`, "(1 (2 3))"},
		{`((lambda (a . rest) (list a rest)) 1)`, "(1 ())"},
		// definitions in a body add to the call's frame, not to the closure's names
		{`(define (f x) (define y (* x 2)) (define (g) (+ x y)) (g)) (list (f 1) (f 10))`, "(3 30)"},
		{`(define (h a) (define a 5) a) (list (h 1) (h 2))`, "(5 5)"},
		// set! on a parameter is the call's own
		{`(define (bump n) (set! n (+ n 1)) n) (list (bump 1) (bump 1))`, "(2 2)"},
		// a frame with many bindings
		{`(define (many) ` + defs(40) + ` (+ v0 v39 v20)) (many)`, "59"},
		{`(let* (` + letStar(30) + `) (+ a0 a29))`, "29"},
		// closures over a frame see later definitions in it
		{`(define (mk) (define (even? n) (if (= n 0) #t (odd? (- n 1)))) (define (odd? n) (if (= n 0) #f (even? (- n 1)))) (even? 10)) (mk)`, "#t"},
		// arity errors still name the procedure
		{`(guard (e (#t (cdr (assq 'message e)))) (f))`, `"f: expected 1 argument(s), got 0"`},
	} {
		v, err := in.EvalString(context.Background(), c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got := Write(v); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
}

func defs(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "(define v%d %d) ", i, i)
	}
	return sb.String()
}

func letStar(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "(a%d %d) ", i, i)
	}
	return sb.String()
}

// Apply takes its arguments by reference from a handler, which keeps using the
// slice; a callee that assigns to its parameter must not change it.
func TestApplyDoesNotAliasTheCallersArguments(t *testing.T) {
	in := New()
	proc, err := in.EvalString(context.Background(), `(lambda (a b) (set! a 99) (set! b 98) 'done)`)
	if err != nil {
		t.Fatal(err)
	}
	args := []Value{Int(1), Int(2)}
	if _, err := in.Apply(context.Background(), proc, args); err != nil {
		t.Fatal(err)
	}
	if args[0].I != 1 || args[1].I != 2 {
		t.Errorf("the callee changed the caller's arguments: %v", args)
	}
}

func TestEnvSnapshotRestoreWithManyBindings(t *testing.T) {
	e := NewEnv(NewEnv(nil))
	for i := 0; i < 40; i++ {
		e.Define(fmt.Sprintf("n%d", i), Int(int64(i)))
	}
	snap := e.Snapshot()
	e.Define("extra", Int(1))
	e.Set("n3", Int(300))
	e.Restore(snap)
	if _, ok := e.Lookup("extra"); ok {
		t.Error("extra survived the restore")
	}
	if v, _ := e.Lookup("n3"); v.I != 3 {
		t.Errorf("n3 = %v", v)
	}
	if v, _ := e.Lookup("n39"); v.I != 39 {
		t.Errorf("n39 = %v", v)
	}
}

// A form's argument list is computed once and shared, so evaluating it twice
// from two goroutines must agree and never change it.
func TestCachedArgumentsAreStable(t *testing.T) {
	forms, err := Read("(+ 1 2 3)")
	if err != nil {
		t.Fatal(err)
	}
	p := forms[0].P
	a, ok := p.argSlice()
	if !ok || len(a) != 3 {
		t.Fatalf("got %v %v", a, ok)
	}
	b, _ := p.argSlice()
	if &a[0] != &b[0] {
		t.Error("the argument list was built twice")
	}
	improper, _ := Read("(f 1 . 2)")
	if _, ok := improper[0].P.argSlice(); ok {
		t.Error("an improper list reported as proper")
	}
	if got, ok := (&Pair{Car: Sym("f"), Cdr: Nil}).argSlice(); !ok || len(got) != 0 {
		t.Errorf("no arguments: %v %v", got, ok)
	}
}
