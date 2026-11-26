package std

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

const procPolicy = `(policy (exec (allow "sh" "cat" "printf" "sort" "seq" "sleep" "echo" "true") (timeout 1m) (max-output 4MB)))`

// A line is there to read as soon as the program writes it, not when it ends.
func TestProcStreamsLines(t *testing.T) {
	needUnix(t)
	start := time.Now()
	got, err := run(t, procPolicy, `(let ((p (proc-start '("sh" "-c" "echo first; sleep 30; echo second"))))
	  (let ((l (proc-read-line p))) (proc-kill! p) (list l (assoc-ref (proc-wait p) "exit-code"))))`)
	if err != nil || got != `("first" -1)` || time.Since(start) > 10*time.Second {
		t.Errorf("got %s, %v after %v", got, err, time.Since(start))
	}
	expect(t, procPolicy, map[string]string{
		`(let ((p (proc-start '("sh" "-c" "echo a; printf 'b\\r\\nc'")))) (list (proc-read-line p) (proc-read-line p) (proc-read-line p) (eof-object? (proc-read-line p)) (eof-object? (proc-read-line p))))`: `("a" "b" "c" #t #t)`,
		`(let ((p (proc-start '("seq" "3")))) (list (proc-read-line p) (assoc-ref (proc-wait p) "stdout")))`:                                                                                                  `("1" "2\n3\n")`,
		`(let ((p (proc-start '("echo" "x")))) (list (tainted? (proc-read-line p)) (tainted? (proc-wait p))))`:                                                                                                `(#t #t)`,
		`(let ((p (proc-start '("sh" "-c" "echo e >&2; exit 3")))) (let ((r (proc-wait p))) (list (assoc-ref r "exit-code") (assoc-ref r "stderr") (eq? r (proc-wait p)))))`:                                  `(3 "e\n" #t)`,
		`(list (proc? (proc-start '("echo"))) (proc? 5))`: `(#t #f)`,
		// More than a pipe holds, never read: waiting drains it rather than deadlocking.
		`(string-length (assoc-ref (proc-wait (proc-start '("seq" "1" "200000"))) "stdout"))`: `1288895`,
	})
}

func TestProcInput(t *testing.T) {
	needUnix(t)
	expect(t, procPolicy, map[string]string{
		`(let ((p (proc-start '("cat") '(("stdin" . pipe))))) (proc-write p "x\n") (let ((a (proc-read-line p))) (proc-write p "y") (proc-close-input p) (proc-close-input p) (list a (proc-read-line p) (assoc-ref (proc-wait p) "exit-code"))))`: `("x" "y" 0)`,
		`(assoc-ref (proc-wait (proc-start '("cat") '(("stdin" . "fed")))) "stdout")`: `"fed"`,
		`(assoc-ref (proc-wait (proc-start '("cat"))) "stdout")`:                      `""`,
	})
	expectErr(t, procPolicy, map[string]string{
		`(proc-write (proc-start '("cat")) "x")`:                                                        "not a pipe",
		`(let ((p (proc-start '("cat") '(("stdin" . pipe))))) (proc-close-input p) (proc-write p "x"))`: "is closed",
		`(let ((p (proc-start '("true") '(("stdin" . pipe))))) (proc-wait p) (proc-write p "x"))`:       "is closed",
		`(proc-start '("cat") '(("stdin" . 5)))`:                                                        `"stdin" takes`,
		`(proc-start '("cat") '(("stdin" . other)))`:                                                    `"stdin" takes`,
		`(proc-start '("cat") '(("cwd" . "/")))`:                                                        "unknown option",
		`(proc-read-line 5)`:                                                                            "expected a process",
	})
}

// A process's output can be the next one's input, as in a shell pipeline,
// and once it is, nobody else can read it.
func TestProcPipelines(t *testing.T) {
	needUnix(t)
	expect(t, procPolicy, map[string]string{
		`(let* ((a (proc-start '("printf" "c\\nb\\na\\n"))) (b (proc-start '("sort") (list (cons "stdin" a))))) (list (assoc-ref (proc-wait b) "stdout") (assoc-ref (proc-wait a) "exit-code") (assoc-ref (proc-wait a) "stdout")))`: `("a\nb\nc\n" 0 "")`,
		// The reader quits early: the writer is not left blocked on a full pipe.
		`(let* ((a (proc-start '("seq" "1" "1000000"))) (b (proc-start '("sh" "-c" "read x; echo $x") (list (cons "stdin" a))))) (list (assoc-ref (proc-wait b) "stdout") (assoc-ref (proc-wait a) "exit-code")))`: `("1\n" -1)`,
	})
	expectErr(t, procPolicy, map[string]string{
		`(let* ((a (proc-start '("echo" "x"))) (b (proc-start '("cat") (list (cons "stdin" a))))) (proc-read-line a))`:                            "goes to another process",
		`(let* ((a (proc-start '("echo" "x"))) (b (proc-start '("cat") (list (cons "stdin" a))))) (proc-start '("cat") (list (cons "stdin" a))))`: "goes to another process already",
		`(let ((a (proc-start '("seq" "2")))) (proc-read-line a) (proc-start '("cat") (list (cons "stdin" a))))`:                                  "already been read",
	})
}

func TestProcGrantAndLimits(t *testing.T) {
	needUnix(t)
	expectErr(t, `(policy (exec (allow "echo") (timeout 1s)))`, map[string]string{
		`(proc-start '("ls"))`:                      "not a command",
		`(proc-start '("echo" 5))`:                  "expected a string",
		`(proc-start '())`:                          "expected a command",
		`(proc-start '("echo") '(("timeout" . 5)))`: "more than the",
	})
	start := time.Now()
	_, err := run(t, `(policy (exec (allow "sleep") (timeout 300ms)))`, `(proc-wait (proc-start '("sleep" "30")))`)
	if err == nil || !strings.Contains(err.Error(), "still running after") || time.Since(start) > 5*time.Second {
		t.Errorf("timeout: %v after %v", err, time.Since(start))
	}
	_, err = run(t, `(policy (exec (allow "sh") (max-output 1KB)))`, `(proc-wait (proc-start '("sh" "-c" "while :; do echo xxxxxxxxxxxxxxxx; done")))`)
	if err == nil || !strings.Contains(err.Error(), "more than the 1024 bytes") {
		t.Errorf("output limit: %v", err)
	}
	_, err = run(t, `(policy (exec (allow "sh") (max-output 1KB)))`, `(proc-read-line (proc-start '("sh" "-c" "while :; do printf x; done")))`)
	if err == nil || !strings.Contains(err.Error(), "a line of output is more than") {
		t.Errorf("line limit: %v", err)
	}
	var events []Event
	_, _ = run(t, procPolicy, `(begin (proc-start '("echo" "a")) (proc-start '("ls")))`, WithAudit(func(e Event) { events = append(events, e) }))
	if len(events) != 2 || events[0].Op != "start" || events[0].Target != "echo a" || events[1].Err == nil {
		t.Errorf("events %+v", events)
	}
}

// A read cancelled with its evaluation stops the program, and closing the
// runtime stops what is still running.
func TestProcStopsWithTheEvaluationAndTheRuntime(t *testing.T) {
	needUnix(t)
	in := scheme.New()
	rt, err := Install(in, mustParse(t, procPolicy))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	bg := context.Background()
	if _, err := in.EvalString(bg, `(define p (proc-start '("sleep" "30")))`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bg, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = in.EvalString(ctx, `(proc-read-line p)`)
	if err == nil || scheme.Catchable(ctx, err) || time.Since(start) > 5*time.Second {
		t.Errorf("cancelled read: %v after %v", err, time.Since(start))
	}
	v, err := in.EvalString(bg, `(assoc-ref (proc-wait p) "exit-code")`)
	if err != nil || scheme.Write(v) != "-1" {
		t.Errorf("after a cancelled read: %v, %v; want the program stopped", v, err)
	}

	v, err = in.EvalString(bg, `(proc-start '("sleep" "30"))`)
	if err != nil {
		t.Fatal(err)
	}
	p := scheme.ObjectOf(v).(*procObj)
	start = time.Now()
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	default:
		t.Error("Close returned with the program still running")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Close took %v", time.Since(start))
	}
	if _, err := in.EvalString(bg, `(proc-start '("echo"))`); err == nil {
		t.Error("a process started after Close")
	}
}
