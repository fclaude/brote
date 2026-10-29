package scheme

import (
	"errors"
	"fmt"
	"strings"
)

// Error is a Scheme-level error: what (error ...) raises and what runtime
// failures produce. guard catches it (a denial and a cancellation excepted,
// which are Denied and the context's error); otherwise it unwinds to the host.
type Error struct {
	Message   string
	Irritants []Value
	Cause     error // wrapped host error, if any (e.g. context.Canceled, *Denied)
	// Taint is the provenance of what Message may quote: the arguments of
	// the primitive that failed. guard gives it to the message it hands on.
	Taint Taint

	// Pos is where the form that failed is in its source, nil if the program
	// built the form (or no list was read from text). Trace lists the
	// procedures the error left, innermost first, and Elided how many more it
	// left than Trace keeps. Error does not print them; FormatError does.
	Pos    *Pos
	Trace  []Frame
	Elided int
}

func (e *Error) Error() string {
	if len(e.Irritants) == 0 {
		return e.Message
	}
	parts := make([]string, len(e.Irritants))
	for i, v := range e.Irritants {
		parts[i] = Write(v)
	}
	return e.Message + " " + strings.Join(parts, " ")
}

func (e *Error) Unwrap() error { return e.Cause }

// Denied is the guard's refusal, surfaced to Scheme as "denied: reason".
type Denied struct {
	Prim   string
	Reason string
}

func (d *Denied) Error() string { return "denied: " + d.Reason }

// errorf builds a runtime error "who: message".
func errorf(who, format string, args ...any) *Error {
	return &Error{Message: who + ": " + fmt.Sprintf(format, args...)}
}

// wrap turns a Go error into a Scheme error, preserving it as Cause. A bare
// *Error passes through unchanged so nested applications do not stack
// prefixes; a wrapped one (e.g. "file.scm: <error>") is re-prefixed.
func wrap(who string, err error) *Error {
	if se, ok := err.(*Error); ok {
		return se
	}
	return &Error{Message: who + ": " + err.Error(), Cause: err}
}

// Source names where some Scheme text came from, usually a file. Library is
// set for text that belongs to the host and not to the script (the prelude, the
// Scheme half of a std module): an error raised in it is reported at the
// script's call of it.
type Source struct {
	Name    string
	Library bool
}

// Pos is a place in Scheme text: the line and column (both from 1, the column
// counting characters) where a list opens. The reader records one for each list
// it reads, and an Error carries the one of the form that failed.
type Pos struct {
	Source    *Source // nil for text that has no name
	Line, Col int
}

// File is the name of the text p is in, or "" if it has none.
func (p *Pos) File() string {
	if p == nil || p.Source == nil {
		return ""
	}
	return p.Source.Name
}

// String is "file:line:col", or "line:col" for text with no name.
func (p *Pos) String() string {
	if p == nil {
		return ""
	}
	if f := p.File(); f != "" {
		return fmt.Sprintf("%s:%d:%d", f, p.Line, p.Col)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Col)
}

// Frame is one procedure an error passed through on its way out: the
// procedure's name ("" if it has none) and where in it evaluation was.
type Frame struct {
	Proc string
	Pos  *Pos
}

// MaxTrace is how many frames an Error keeps. A runaway recursion unwinds
// through thousands, and the innermost are the ones worth reading.
const MaxTrace = 16

// annotate returns err with the position of the form x added if it has none,
// and, when fn is the procedure x is in, a frame for it. If x is in library
// code, the position is at instead: the last call from the script that led
// there. It copies the error instead of changing it: one that a task failed
// with is handed to everyone who waits for it.
func annotate(err error, x Value, fn *Closure, at *Pos) error {
	se, ok := err.(*Error)
	if !ok {
		return err
	}
	var loc *Pos
	if x.Kind == KPair {
		loc = x.P.loc
	}
	if loc.isLibrary() {
		loc, fn = at, nil // a library procedure is not in the trace
	}
	if loc == nil {
		return err
	}
	needPos := se.Pos == nil
	needFrame := fn != nil && len(se.Trace) < MaxTrace
	if !needPos && !needFrame {
		if fn != nil {
			cp := *se
			cp.Elided++ // a frame not kept
			return &cp
		}
		return err
	}
	cp := *se
	if needPos {
		cp.Pos = loc
	}
	if needFrame {
		cp.Trace = append(se.Trace[:len(se.Trace):len(se.Trace)], Frame{Proc: fn.DisplayName(), Pos: loc})
	}
	return &cp
}

// FormatError renders err for a person: where it happened, what it was, and the
// procedures it passed through, innermost first. An error with no position is
// just its text.
func FormatError(err error) string {
	var se *Error
	if !errors.As(err, &se) || se.Pos == nil {
		return err.Error()
	}
	var sb strings.Builder
	text := err.Error()
	own := se.Error()
	prefix := ""
	if strings.HasSuffix(text, own) {
		prefix = strings.TrimSuffix(text, own)
	}
	if f := se.Pos.File(); prefix == f+": " {
		prefix = "" // the position already names the file
	}
	sb.WriteString(se.Pos.String() + ": " + prefix + own)
	for i, f := range se.Trace {
		if i == 0 && f.Pos == se.Pos {
			continue // the first frame is the place the error was raised
		}
		sb.WriteString("\n  " + frameText(f))
	}
	if se.Elided > 0 {
		fmt.Fprintf(&sb, "\n  … and %d more", se.Elided)
	}
	return sb.String()
}

func (p *Pos) isLibrary() bool { return p != nil && p.Source != nil && p.Source.Library }
