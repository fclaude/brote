package repl_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/repl"
	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
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

func TestCommands(t *testing.T) {
	got := run(t, ",doc take\n,doc nosuchthing\n(define (f x) \"Double x.\" (* 2 x))\n,defs\n,doc f\n,apropos vector-m\n,time (+ 1 2)\n,bogus\n,quit\n(display \"not reached\")\n", repl.Options{Commands: true})
	for _, want := range []string{
		"(take l n)\nThe first n elements of l.\n",
		"nothing to say about nosuchthing\n",
		"\nf\n",
		"(f x)\nDouble x.\n",
		"vector-map\n",
		"3\ntook ",
		"no command ,bogus; ,help lists them\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "not reached") {
		t.Errorf(",quit did not end the session:\n%s", got)
	}
}

// Without Commands a comma is Scheme's, so a script piped in is not
// reinterpreted.
func TestCommandsAreOffByDefault(t *testing.T) {
	got := run(t, ",help\n", repl.Options{})
	if !strings.HasPrefix(got, "error: ") {
		t.Fatalf("got %q", got)
	}
}

// ,policy prints what the host's -print-policy prints for the same policy:
// the same rendering, std.Policy.Pretty, not a copy of it.
func TestPolicyCommand(t *testing.T) {
	pol := std.Allow(std.Profile("safe"), std.NetHosts("api.example.com"))
	want := pol.Pretty() + "\n"
	got := run(t, ",policy\n", repl.Options{Commands: true, Policy: func() string { return pol.Pretty() }})
	if got != want {
		t.Fatalf(":policy printed %q, want %q", got, want)
	}
	// Without a Policy option there is no ,policy.
	if got := run(t, ",policy\n", repl.Options{Commands: true}); got != "this session has no policy to show\n" {
		t.Fatalf(":policy without a host policy: %q", got)
	}
}

// A comma inside an unfinished form is part of the form.
func TestCommaInsideAFormIsNotACommand(t *testing.T) {
	got := run(t, "(define x 1)\n`(a\n,x)\n", repl.Options{Commands: true})
	if got != "(a 1)\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptStartsALineAfterUnfinishedOutput(t *testing.T) {
	var out bytes.Buffer
	tr := repl.NewTracker(&out)
	in := scheme.New(scheme.WithOutput(tr))
	if err := repl.Run(context.Background(), in, strings.NewReader("(display \"x\")\n(+ 1 2)\n"), tr, repl.Options{Prompt: "> ", Continue: ". ", Tracker: tr}); err != nil {
		t.Fatal(err)
	}
	if want := "> x\n> 3\n> \n"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestCompleter(t *testing.T) {
	in := scheme.New()
	complete := repl.Completer(in)
	for _, c := range []struct {
		line      string
		start     int
		wantAmong []string
		wantNone  []string
	}{
		{"(string-up", 1, []string{"string-upcase"}, []string{"string-append"}},
		{"(map string-ap", 5, []string{"string-append"}, []string{"string-upcase"}},
		{"(if", 1, []string{"if"}, nil},
		{",do", 0, []string{",doc"}, []string{",defs", "doc"}},
	} {
		start, got := complete([]rune(c.line), len([]rune(c.line)))
		if start != c.start {
			t.Errorf("%q: start = %d, want %d", c.line, start, c.start)
		}
		has := map[string]bool{}
		for _, g := range got {
			has[g] = true
		}
		for _, w := range c.wantAmong {
			if !has[w] {
				t.Errorf("%q: no %q in %v", c.line, w, got)
			}
		}
		for _, w := range c.wantNone {
			if has[w] {
				t.Errorf("%q: unexpected %q in %v", c.line, w, got)
			}
		}
	}
	if _, got := complete([]rune("(foo "), 5); len(got) != 0 {
		t.Errorf("an empty word completed to %v", got)
	}
}
