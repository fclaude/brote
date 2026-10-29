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
	"sync"

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

	// Lines, when set, is where lines come from, and the loop reads no input
	// itself: an Editor gives a terminal line editing, completion and history.
	Lines LineReader

	// Commands turns on the lines that start with a comma (",help"), which
	// are for the person at the prompt and not Scheme.
	Commands bool

	// Tracker, if set, is the writer the interpreter's output and w both go
	// through. The loop uses it to start the prompt on a new line when a
	// form printed text without ending it.
	Tracker *Tracker
}

// Tracker passes output on and remembers whether it ended a line.
type Tracker struct {
	mu      sync.Mutex
	w       io.Writer
	atStart bool
}

// NewTracker is a Tracker that writes to w.
func NewTracker(w io.Writer) *Tracker { return &Tracker{w: w, atStart: true} }

func (t *Tracker) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) > 0 {
		t.atStart = p[len(p)-1] == '\n'
	}
	return t.w.Write(p)
}

// lineEntered is called when the person has pressed Enter: the terminal is at
// the start of a line, whatever was written before.
func (t *Tracker) lineEntered() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.atStart = true
}

// AtLineStart reports whether nothing has been written since the last newline.
func (t *Tracker) AtLineStart() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.atStart
}

// scanLines is the LineReader for plain input: it writes the prompt, if there
// is one, and reads a line.
type scanLines struct {
	sc *bufio.Scanner
	w  io.Writer
}

func (s *scanLines) ReadLine(prompt string) (string, error) {
	if prompt != "" {
		fmt.Fprint(s.w, prompt)
	}
	if s.sc.Scan() {
		return s.sc.Text(), nil
	}
	if err := s.sc.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

func (s *scanLines) AddHistory(string) {}

// Run reads forms from r, evaluates them in in, and writes each value that is
// not void, or the error the form raised, to w until r ends. Input that stops
// short, an open list or string, continues on the next line. An error does
// not end the session: it is written, the rest of the line is dropped, and
// the loop reads on. Run returns the reader's error, if any.
func Run(ctx context.Context, in *scheme.Interp, r io.Reader, w io.Writer, o Options) error {
	lines := o.Lines
	var sc *bufio.Scanner
	if lines == nil {
		sc = bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		lines = &scanLines{sc: sc, w: w}
	}
	startLine := func() {
		if o.Tracker != nil && !o.Tracker.AtLineStart() {
			fmt.Fprintln(w)
		}
	}
	cmds := newCommands(in, ctx, w, o)
	var pending strings.Builder
	prompt := o.Prompt
	for {
		if pending.Len() == 0 {
			startLine()
		}
		line, err := lines.ReadLine(prompt)
		if errors.Is(err, ErrInterrupt) {
			pending.Reset()
			prompt = o.Prompt
			continue
		}
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
		if o.Tracker != nil {
			o.Tracker.lineEntered()
		}
		if o.Commands && pending.Len() == 0 && strings.HasPrefix(strings.TrimSpace(line), ",") {
			lines.AddHistory(line)
			if quit := cmds.run(strings.TrimSpace(line)); quit {
				return nil
			}
			prompt = o.Prompt
			continue
		}
		if pending.Len() > 0 {
			pending.WriteByte('\n')
		}
		pending.WriteString(line)
		forms, err := scheme.ReadNamed("<repl>", pending.String())
		if errors.Is(err, scheme.ErrIncomplete) {
			prompt = o.Continue
			continue
		}
		lines.AddHistory(strings.ReplaceAll(pending.String(), "\n", " "))
		pending.Reset()
		prompt = o.Prompt
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			continue
		}
		evalCtx, release := ctx, context.CancelFunc(func() {})
		if o.EvalContext != nil {
			evalCtx, release = o.EvalContext(ctx)
		}
		for _, f := range forms {
			v, err := in.Eval(evalCtx, f, in.Global())
			if err != nil {
				startLine()
				fmt.Fprintf(w, "error: %s\n", Describe(err))
				break
			}
			if v.Kind != scheme.KVoid {
				startLine()
				fmt.Fprintln(w, scheme.Write(v))
			}
		}
		release()
	}
	if o.Prompt != "" {
		fmt.Fprintln(w)
	}
	if sc != nil {
		return sc.Err()
	}
	return nil
}

// Describe is err as the loop writes it, and as the command writes the error
// of -e. A one-line form that failed in itself needs no position (it is the
// line just typed); one that failed inside a procedure, or on a later line,
// says where.
func Describe(err error) string {
	var se *scheme.Error
	if errors.As(err, &se) && se.Pos != nil && se.Pos.Line == 1 && len(se.Trace) == 0 {
		return err.Error()
	}
	return scheme.FormatError(err)
}
