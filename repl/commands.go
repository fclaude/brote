package repl

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/fclaude/brote/scheme"
)

// commandHelp lists the commands, in the order ,help shows them.
var commandHelp = []struct{ name, args, text string }{
	{",help", "", "show this list"},
	{",doc", "NAME", "how NAME is called, and what it does"},
	{",apropos", "TEXT", "the procedures and special forms with TEXT in their name"},
	{",defs", "", "the names this session has defined"},
	{",load", "FILE", "evaluate the forms in FILE"},
	{",time", "EXPR", "evaluate EXPR and say how long it took"},
	{",quit", "", "leave (Ctrl-D does too)"},
}

// commands is the lines that start with a comma, for the person at the prompt.
type commands struct {
	in    *scheme.Interp
	ctx   context.Context
	w     io.Writer
	o     Options
	known map[string]bool // the names that were there when the session began
}

func newCommands(in *scheme.Interp, ctx context.Context, w io.Writer, o Options) *commands {
	known := map[string]bool{}
	for _, n := range in.Names() {
		known[n] = true
	}
	return &commands{in: in, ctx: ctx, w: w, o: o, known: known}
}

// run does the command in line and reports whether the session is over.
func (c *commands) run(line string) (quit bool) {
	name, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case ",help", ",?":
		for _, h := range commandHelp {
			usage := strings.TrimSpace(h.name + " " + h.args)
			fmt.Fprintf(c.w, "  %-16s %s\n", usage, h.text)
		}
	case ",doc":
		if arg == "" {
			fmt.Fprintln(c.w, "usage: ,doc NAME")
			break
		}
		if d, ok := c.in.Doc(arg); ok {
			fmt.Fprintln(c.w, d)
		} else {
			fmt.Fprintf(c.w, "nothing to say about %s\n", arg)
		}
	case ",apropos":
		if arg == "" {
			fmt.Fprintln(c.w, "usage: ,apropos TEXT")
			break
		}
		var names []string
		for _, n := range c.in.Names() {
			if _, ok := c.in.Doc(n); ok && strings.Contains(n, arg) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			fmt.Fprintf(c.w, "no name has %q in it\n", arg)
		} else {
			fmt.Fprintln(c.w, strings.Join(names, " "))
		}
	case ",defs":
		var names []string
		for n := range c.in.Global().Snapshot() {
			if !c.known[n] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			fmt.Fprintln(c.w, "nothing defined yet")
		} else {
			fmt.Fprintln(c.w, strings.Join(names, " "))
		}
	case ",load":
		if arg == "" {
			fmt.Fprintln(c.w, "usage: ,load FILE")
			break
		}
		ctx, release := c.evalContext()
		defer release()
		if err := c.in.LoadFile(ctx, arg); err != nil {
			fmt.Fprintf(c.w, "error: %s\n", scheme.FormatError(err))
		}
	case ",time":
		if arg == "" {
			fmt.Fprintln(c.w, "usage: ,time EXPR")
			break
		}
		ctx, release := c.evalContext()
		defer release()
		start := time.Now()
		v, err := c.in.EvalNamed(ctx, "<repl>", arg)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Fprintf(c.w, "error: %s\n", Describe(err))
		} else if v.Kind != scheme.KVoid {
			fmt.Fprintln(c.w, scheme.Write(v))
		}
		fmt.Fprintf(c.w, "took %s\n", elapsed.Round(time.Microsecond))
	case ",quit", ",q", ",exit":
		return true
	default:
		fmt.Fprintf(c.w, "no command %s; ,help lists them\n", name)
	}
	return false
}

func (c *commands) evalContext() (context.Context, context.CancelFunc) {
	if c.o.EvalContext != nil {
		return c.o.EvalContext(c.ctx)
	}
	return c.ctx, func() {}
}

// Completer is the Editor.Complete for in: the word before the cursor is
// finished from the names in the global environment, the primitives and the
// special forms, or from the commands if the line starts with a comma.
func Completer(in *scheme.Interp) func(line []rune, pos int) (int, []string) {
	return func(line []rune, pos int) (int, []string) {
		start := pos
		for start > 0 && isNameRune(line[start-1]) {
			start--
		}
		word := string(line[start:pos])
		if strings.HasPrefix(strings.TrimLeftFunc(string(line), unicode.IsSpace), ",") && start == strings.Index(string(line), ",") {
			var out []string
			for _, h := range commandHelp {
				if strings.HasPrefix(h.name, word) {
					out = append(out, h.name)
				}
			}
			return start, out
		}
		if word == "" {
			return start, nil
		}
		var out []string
		for _, n := range in.Names() {
			if strings.HasPrefix(n, word) {
				out = append(out, n)
			}
		}
		return start, out
	}
}

func isNameRune(r rune) bool {
	return !unicode.IsSpace(r) && !strings.ContainsRune("()'\"`;", r)
}
