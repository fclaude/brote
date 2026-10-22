// Package repl is a read-eval-print loop over a scheme.Interp: the loop of
// the brote command, for any host that wants to offer one.
package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fclaude/brote/scheme"
)

// Options says how the loop talks to the person at the other end.
type Options struct {
	// Prompt is written before each form is read, and Continue while a form
	// is unfinished. Empty means nothing is written, as for a pipe or a
	// script; with a Prompt, the loop ends the line when the input does.
	Prompt, Continue string

	// EvalContext, when set, gives the context one form is evaluated in and
	// the function that releases it. The command uses it to let an interrupt
	// stop a form that never ends without ending the session.
	EvalContext func(parent context.Context) (context.Context, context.CancelFunc)
}

// Run reads forms from r, evaluates them in in, and writes each value that is
// not void, or the error the form raised, to w until r ends. Input that stops
// short, an open list or string, continues on the next line. An error does
// not end the session: it is written, the rest of the line is dropped, and
// the loop reads on. Run returns the reader's error, if any.
func Run(ctx context.Context, in *scheme.Interp, r io.Reader, w io.Writer, o Options) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var pending strings.Builder
	show := func(p string) {
		if p != "" {
			fmt.Fprint(w, p)
		}
	}
	show(o.Prompt)
	for sc.Scan() {
		if pending.Len() > 0 {
			pending.WriteByte('\n')
		}
		pending.WriteString(sc.Text())
		forms, err := scheme.Read(pending.String())
		if errors.Is(err, scheme.ErrIncomplete) {
			show(o.Continue)
			continue
		}
		pending.Reset()
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			show(o.Prompt)
			continue
		}
		evalCtx, release := ctx, context.CancelFunc(func() {})
		if o.EvalContext != nil {
			evalCtx, release = o.EvalContext(ctx)
		}
		for _, f := range forms {
			v, err := in.Eval(evalCtx, f, in.Global())
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				break
			}
			if v.Kind != scheme.KVoid {
				fmt.Fprintln(w, scheme.Write(v))
			}
		}
		release()
		show(o.Prompt)
	}
	if o.Prompt != "" {
		fmt.Fprintln(w)
	}
	return sc.Err()
}
