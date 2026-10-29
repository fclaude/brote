package scheme

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func evalNamedErr(t *testing.T, name, src string) *Error {
	t.Helper()
	in := New()
	_, err := in.EvalNamed(context.Background(), name, src)
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want a Scheme error, got %v", err)
	}
	return se
}

func TestErrorPosition(t *testing.T) {
	se := evalNamedErr(t, "a.scm", "(define x 1)\n\n  (car 5)")
	if got := se.Pos.String(); got != "a.scm:3:3" {
		t.Errorf("Pos = %s, want a.scm:3:3", got)
	}
	// Text with no name has a bare line and column.
	se = evalNamedErr(t, "", "(+ 1\n   (car 5))")
	if got := se.Pos.String(); got != "2:4" {
		t.Errorf("Pos = %s, want 2:4", got)
	}
}

func TestErrorPositionOfUnboundVariable(t *testing.T) {
	// A symbol has no position of its own; the list around it does.
	se := evalNamedErr(t, "a.scm", "(list 1\n  (+ 2 nope))")
	if got := se.Pos.String(); got != "a.scm:2:3" {
		t.Errorf("Pos = %s, want a.scm:2:3", got)
	}
}

func TestErrorTrace(t *testing.T) {
	src := `(define (g x) (+ 1 (car x)))
(define (f x) (* 2 (g x)))
(f 5)`
	se := evalNamedErr(t, "a.scm", src)
	var names []string
	for _, f := range se.Trace {
		names = append(names, f.Proc)
	}
	if strings.Join(names, ",") != "g,f" {
		t.Fatalf("trace = %v, want g,f", names)
	}
	out := FormatError(se)
	for _, want := range []string{"a.scm:1:20: car:", "in g (a.scm:1:15)", "in f (a.scm:2:15)"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatError missing %q in:\n%s", want, out)
		}
	}
}

func TestTraceKeepsOnlyTheProceduresStillOnTheStack(t *testing.T) {
	// A tail call replaces its caller, so the trace does not name it.
	src := `(define (g x) (car x))
(define (f x) (g x))
(f 5)`
	se := evalNamedErr(t, "a.scm", src)
	if len(se.Trace) != 1 || se.Trace[0].Proc != "g" {
		t.Fatalf("trace = %+v, want only g", se.Trace)
	}
}

func TestTraceThroughAPrimitive(t *testing.T) {
	se := evalNamedErr(t, "a.scm", "(define (h x) (car x))\n(map h '(1 2))")
	if len(se.Trace) == 0 || se.Trace[0].Proc != "h" {
		t.Fatalf("trace = %+v, want h first", se.Trace)
	}
}

func TestTraceIsBounded(t *testing.T) {
	src := `(define (down n) (+ 1 (if (= n 0) (car 0) (down (- n 1)))))
(down 500)`
	se := evalNamedErr(t, "a.scm", src)
	if len(se.Trace) != MaxTrace {
		t.Errorf("len(Trace) = %d, want %d", len(se.Trace), MaxTrace)
	}
	if want := 501 - MaxTrace; se.Elided != want {
		t.Errorf("Elided = %d, want %d", se.Elided, want)
	}
	if out := FormatError(se); !strings.Contains(out, "more") {
		t.Errorf("FormatError does not say frames were dropped:\n%s", out)
	}
}

func TestGuardConditionHasWhereAndTrace(t *testing.T) {
	in := New()
	v, err := in.EvalNamed(context.Background(), "a.scm", `(define (f x) (+ 1 (car x)))
(guard (e (#t (list (assoc-ref e 'where) (assoc-ref e 'trace)))) (f 5))`)
	if err != nil {
		t.Fatal(err)
	}
	want := `("a.scm:1:20" ("in f (a.scm:1:15)"))`
	if got := Write(v); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestErrorWithoutAPositionIsPlain(t *testing.T) {
	// A form the program built has no position.
	in := New()
	form := List(Sym("car"), Int(1))
	_, err := in.Eval(context.Background(), form, in.Global())
	var se *Error
	if !errors.As(err, &se) || se.Pos != nil {
		t.Fatalf("want an error with no position, got %+v", se)
	}
	if FormatError(err) != err.Error() {
		t.Errorf("FormatError = %q, want %q", FormatError(err), err.Error())
	}
}

func TestLoadFileErrorNamesTheFileOnce(t *testing.T) {
	path := t.TempDir() + "/boom.scm"
	if err := os.WriteFile(path, []byte("(define (f) (error \"boom\" 1))\n(f)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := New()
	err := in.LoadFile(context.Background(), path)
	if err == nil {
		t.Fatal("want an error")
	}
	out := FormatError(err)
	if !strings.HasPrefix(out, path+":1:") || strings.Contains(out, path+": ") {
		t.Errorf("FormatError = %q", out)
	}
}

func TestSharedTaskErrorIsNotChanged(t *testing.T) {
	// Every task that waits for a failed task is handed the same error; the
	// frames each adds on the way out must not show up in the others.
	in := New(WithTasks(TaskLimits{}))
	src := `(define (boom) (car 1))
(define t (spawn boom))
(define (wait-it) (task-wait t))
(list (guard (e (#t (length (assoc-ref e 'trace)))) (wait-it))
      (guard (e (#t (length (assoc-ref e 'trace)))) (task-wait t)))`
	v, err := in.EvalNamed(context.Background(), "a.scm", src)
	if err != nil {
		t.Fatal(err)
	}
	if got := Write(v); got != "(2 1)" {
		t.Errorf("trace lengths = %s, want (2 1)", got)
	}
}

func TestErrorInTheLibraryIsReportedAtTheCall(t *testing.T) {
	// take is the prelude's; the person who wrote (take …) wants to be told
	// about that line and not about the prelude's.
	se := evalNamedErr(t, "a.scm", "(define x 1)\n  (take '(1) 8)")
	if got := se.Pos.String(); got != "a.scm:2:3" {
		t.Errorf("Pos = %s, want a.scm:2:3", got)
	}
	if len(se.Trace) != 0 {
		t.Errorf("trace = %+v, want none", se.Trace)
	}
}
