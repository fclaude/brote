package scheme

import (
	"context"
	"testing"
)

func TestTestFramework(t *testing.T) {
	in := New()
	got := evalAll(t, in, `
(define (f x) (* 2 x))
(test "passes" (assert-equal 4 (f 2)))
(test "equal fails" (assert-equal 5 (f 2)))
(test "assert fails" (assert (< 3 (f 1))))
(test "assert with a message" (assert #f "custom:"))
(test "errors as expected" (assert-error (car 5)))
(test "errors with text" (assert-error (error "boom" 7) "boom"))
(test "no error" (assert-error (f 1)))
(test "wrong text" (assert-error (error "boom") "bang"))
(test "raises" (error "oops" 'a "b"))
(test "returns normally" 'anything)
(map (lambda (r) (list (car r) (cadr r))) (test-results))`)
	want := `(("passes" #t) ("equal fails" #f) ("assert fails" #f) ("assert with a message" #f) ("errors as expected" #t) ("errors with text" #t) ("no error" #f) ("wrong text" #f) ("raises" #f) ("returns normally" #t))`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestTestFailureMessages(t *testing.T) {
	in := New()
	if _, err := in.EvalNamed(context.Background(), "f_test.scm", `
(define (f x) (* 2 x))
(test "equal" (assert-equal 5 (f 2)))
(test "assert" (assert (< 3 (f 1))))
(test "raised" (error "oops" 'a "b"))`); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"equal":  `assert-equal failed: (f 2) "expected" 5 "got" 4`,
		"assert": `assertion failed: (< 3 (f 1))`,
		"raised": `oops a "b"`,
	} {
		got := evalAll(t, in, `(cadr (cdr (assoc "`+name+`" (test-results))))`)
		if got != Write(Str(want)) {
			t.Errorf("%s: message = %s, want %s", name, got, Write(Str(want)))
		}
	}
	// A failure says where, at the assertion and not inside the macro.
	if got := evalAll(t, in, `(list-ref (assoc "equal" (test-results)) 3)`); got != `"f_test.scm:3:15"` {
		t.Errorf("where = %s", got)
	}
	if got := evalAll(t, in, `(list-ref (assoc "assert" (test-results)) 3)`); got != `"f_test.scm:4:16"` {
		t.Errorf("where = %s", got)
	}
}

func TestAssertOutsideATestRaises(t *testing.T) {
	_, err := New().EvalString(context.Background(), `(assert (= 1 2))`)
	if err == nil {
		t.Fatal("want an error")
	}
}

func TestTestRecordsAFailureButNotACancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := New()
	in.mustRegister("stop-now", "Cancel the evaluation.", nil, false, func(c *Call) (Value, error) {
		cancel()
		return Void, nil
	})
	_, err := in.EvalString(ctx, `(test "cancelled" (stop-now) (let loop () (loop)))`)
	if err == nil {
		t.Fatal("a cancelled test was recorded as a failure and the evaluation went on")
	}
}
