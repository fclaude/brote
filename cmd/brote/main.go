// Command brote is the standalone Brote Scheme interpreter: a REPL, a script
// runner and an expression evaluator over the same interpreter a Go program
// embeds. It has the language's builtins, `load`, and three host primitives
// for scripts (command-line, read-file, read-line; host.go), and no gate.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/fclaude/brote/repl"
	"github.com/fclaude/brote/scheme"
)

const usage = `usage: brote [-e EXPR | FILE...] [-- ARG...]
       brote -version

With no arguments brote reads Scheme from standard input and evaluates it,
printing each value that is not void. With a terminal on standard input it
prompts, and an interrupt (Ctrl-C) stops the form being evaluated and not the
session.

  FILE...     evaluate the files in order, in one interpreter
  -e EXPR     evaluate EXPR and print its value unless it is void
  -- ARG...   arguments for the script: (command-line) returns them as strings
  -version    print the version

A script can also (read-file path) into a string and (read-line) from standard
input, which gives #f once there is no more; neither is there in the REPL.

An error is written to standard error and ends the run with status 1.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is main with its streams made explicit, so a test can drive it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	args, scriptArgs := splitScriptArgs(args)
	fs := flag.NewFlagSet("brote", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	expr := fs.String("e", "", "")
	showVersion := fs.Bool("version", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		fmt.Fprintf(stderr, "brote: %v\n\n%s", err, usage)
		return 2
	}
	files := fs.Args()
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	if *showVersion {
		fmt.Fprintln(stdout, "brote", version())
		return 0
	}
	if given["e"] && len(files) > 0 {
		fmt.Fprintf(stderr, "brote: -e takes an expression, not files\n\n%s", usage)
		return 2
	}

	in := scheme.New(scheme.WithOutput(stdout))
	interactive := !given["e"] && len(files) == 0
	if err := registerHost(in, scriptArgs, stdin, !interactive); err != nil {
		fmt.Fprintf(stderr, "brote: %v\n", err)
		return 1
	}
	ctx := context.Background()
	switch {
	case given["e"]:
		v, err := in.EvalString(ctx, *expr)
		if err != nil {
			fmt.Fprintf(stderr, "brote: %v\n", err)
			return 1
		}
		if v.Kind != scheme.KVoid {
			fmt.Fprintln(stdout, scheme.Write(v))
		}
	case len(files) > 0:
		for _, path := range files {
			if err := in.LoadFile(ctx, path); err != nil {
				fmt.Fprintf(stderr, "brote: %v\n", err)
				return 1
			}
		}
	default:
		o := repl.Options{}
		if isTerminal(stdin) {
			o.Prompt, o.Continue = "brote> ", "...    "
			o.EvalContext = func(parent context.Context) (context.Context, context.CancelFunc) {
				return signal.NotifyContext(parent, os.Interrupt)
			}
		}
		if err := repl.Run(ctx, in, stdin, stdout, o); err != nil {
			fmt.Fprintf(stderr, "brote: %v\n", err)
			return 1
		}
	}
	return 0
}

// isTerminal reports whether r is a character device, which is what a
// terminal is; the standard library has no closer test, and a wrong answer
// only adds or drops prompts.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// version is the module version the binary was built from, with the commit
// when the build recorded one.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	v := info.Main.Version
	if v == "" {
		v = "(devel)"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	// A pseudo-version already holds the commit and +dirty; a release tag or
	// "(devel)" does not.
	if rev != "" && !strings.Contains(v, rev) {
		v += " (" + rev + dirty + ")"
	}
	return v
}
