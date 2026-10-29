// Command brote is the standalone Brote Scheme interpreter: a REPL, a script
// runner and an expression evaluator over the same interpreter a Go program
// embeds. It has the language's builtins, `load`, three host primitives for
// scripts (command-line, read-file, read-line; host.go) and the extended
// library of package std under a policy (policy.go). By default a script may
// reach everything the person running it can; -sandbox, -policy, -allow and
// -deny narrow that.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/fclaude/brote/repl"
	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
	buildversion "github.com/fclaude/brote/version"
)

const usage = `usage: brote [OPTION...] [-e EXPR | FILE...] [-- ARG...]
       brote -version

With no arguments brote reads Scheme from standard input and evaluates it,
printing each value that is not void. With a terminal on standard input it
prompts, and an interrupt (Ctrl-C) stops the form being evaluated and not the
session.

  FILE...     evaluate the files in order, in one interpreter
  -e EXPR     evaluate EXPR and print its value unless it is void
  -- ARG...   arguments for the script: (command-line) returns them as strings
  -version    print the version

What a script can reach (the standard library's modules; see docs/capabilities.md):

  By default every module is available: files, network, processes, environment,
  clock, randomness, regexp, encodings, digests, paths.

  -sandbox         start from the safe set instead: pure computation, the clock
                   and randomness, and nothing that touches the machine
  -policy FILE     start from the policy written in FILE
  -allow SPEC      grant something; repeatable. Naming one switches the start
                   from everything to the safe set. For example
                     -allow fs.read=data   -allow fs.write=out
                     -allow net=api.example.com   -allow exec=git   -allow env=HOME
  -deny MODULE     take a module away from whatever the start was; repeatable
  -print-policy    show what the script would be granted and exit

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
	sandbox := fs.Bool("sandbox", false, "")
	policyFile := fs.String("policy", "", "")
	printPolicy := fs.Bool("print-policy", false, "")
	var allow, deny listFlag
	fs.Var(&allow, "allow", "")
	fs.Var(&deny, "deny", "")
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
		fmt.Fprintln(stdout, buildversion.Line("brote"))
		return 0
	}
	if given["e"] && len(files) > 0 {
		fmt.Fprintf(stderr, "brote: -e takes an expression, not files\n\n%s", usage)
		return 2
	}

	pol, err := buildPolicy(*sandbox, *policyFile, allow, deny)
	if err != nil {
		fmt.Fprintf(stderr, "brote: %v\n", err)
		return 2
	}
	if *printPolicy {
		fmt.Fprintln(stdout, pol.Pretty())
		return 0
	}

	in := scheme.New(scheme.WithOutput(stdout))
	rt, err := std.Install(in, pol)
	if err != nil {
		fmt.Fprintf(stderr, "brote: %v\n", err)
		return 1
	}
	defer rt.Close()
	if *sandbox || *policyFile != "" || len(allow) > 0 || len(deny) > 0 {
		// load would otherwise read any file the person can; keep it inside the grant.
		in.SetGate(rt.Gate(nil))
	}
	interactive := !given["e"] && len(files) == 0
	if err := registerHost(in, rt, scriptArgs, stdin, !interactive); err != nil {
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
