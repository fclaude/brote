package repl_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/repl"
	"github.com/fclaude/brote/scheme"
)

func run(t *testing.T, input string, o repl.Options) string {
	t.Helper()
	var out bytes.Buffer
	in := scheme.New(scheme.WithOutput(&out))
	if err := repl.Run(context.Background(), in, strings.NewReader(input), &out, o); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestValuesPrintAndVoidDoesNot(t *testing.T) {
	got := run(t, "(+ 1 2)\n(define x 5)\n\"s\"\n(display \"d\")\n", repl.Options{})
	if want := "3\n\"s\"\nd"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDefinitionsLiveAcrossLines(t *testing.T) {
	got := run(t, "(define (f n) (* n 2))\n(f 21)\n", repl.Options{})
	if got != "42\n" {
		t.Fatalf("got %q", got)
	}
}

// An unfinished form continues on the next line, and the session says so
// with the continuation prompt.
func TestIncompleteInputContinues(t *testing.T) {
	got := run(t, "(+ 1\n   2)\n", repl.Options{Prompt: "> ", Continue: ".. "})
	if want := "> .. 3\n> \n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// An error is written and the session goes on; the rest of that line's forms
// are dropped, the next line's are not.
func TestErrorDoesNotEndTheSession(t *testing.T) {
	got := run(t, "(car 5) (display \"never\")\n(+ 1 1)\n(+ 1 2))\n", repl.Options{})
	if !strings.HasPrefix(got, "error: ") || strings.Contains(got, "never") || !strings.Contains(got, "\n2\n") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "error: ") || strings.Count(got, "error: ") != 2 {
		t.Fatalf("the unbalanced line was not reported:\n%s", got)
	}
}

func TestNoPromptNoNewlineAtEnd(t *testing.T) {
	if got := run(t, "1\n", repl.Options{}); got != "1\n" {
		t.Fatalf("got %q", got)
	}
}

// EvalContext lets a host stop one form without ending the loop.
func TestEvalContextBoundsOneForm(t *testing.T) {
	o := repl.Options{EvalContext: func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, 50*time.Millisecond)
	}}
	got := run(t, "(let loop () (loop))\n(+ 1 2)\n", o)
	if !strings.HasPrefix(got, "error: ") || !strings.HasSuffix(got, "\n3\n") {
		t.Fatalf("got %q", got)
	}
}
