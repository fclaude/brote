package scheme_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fclaude/brote/scheme"
)

// Evaluate a program and print its value. Output written by the program goes
// where WithOutput says; without it, nowhere.
func Example() {
	in := scheme.New(scheme.WithOutput(os.Stdout))
	v, err := in.EvalString(context.Background(), `
		(define (square x) (* x x))
		(display "squares:")
		(newline)
		(map square '(1 2 3))`)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(scheme.Write(v))
	// Output:
	// squares:
	// (1 4 9)
}

// A host operation is a Go function behind a name, a doc line and typed
// parameters. The interpreter checks the argument count before the handler
// runs; the handler checks types as it reads each argument.
func ExampleInterp_Register() {
	in := scheme.New()
	err := in.Register(&scheme.Prim{
		Name: "shout",
		Meta: scheme.Meta{
			Doc: "Upper-case a string and end it with exclamation marks.",
			Params: []scheme.Param{
				{Name: "text", Type: "string", Doc: "the text to shout"},
				{Name: "marks", Type: "integer", Doc: "how many marks (default 1)", Optional: true},
			},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			text, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			marks := int64(1)
			if len(c.Args) > 1 {
				if marks, err = c.Int(1); err != nil {
					return scheme.Value{}, err
				}
			}
			return scheme.Str(strings.ToUpper(text) + strings.Repeat("!", int(marks))), nil
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	v, _ := in.EvalString(context.Background(), `(list (shout "hi") (shout "hi" 3))`)
	fmt.Println(scheme.Write(v))
	_, err = in.EvalString(context.Background(), `(shout 42)`)
	fmt.Println(err)
	// Output:
	// ("HI!" "HI!!!")
	// shout: argument 1: expected string, got integer 42
}

// A closure the program defined outlives the evaluation that made it, so a
// host can keep one and call it later with values of its own: an editor
// command, a callback, a hook.
func ExampleInterp_Apply() {
	in := scheme.New()
	ctx := context.Background()
	if _, err := in.EvalString(ctx, `
		(define total 0)
		(define (add! n) (set! total (+ total n)) total)`); err != nil {
		fmt.Println(err)
		return
	}
	add, _ := in.Global().Lookup("add!")
	for _, n := range []int64{1, 2, 3} {
		v, err := in.Apply(ctx, add, []scheme.Value{scheme.Int(n)})
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(scheme.Write(v))
	}
	// Output:
	// 1
	// 3
	// 6
}

// What EvalString returns for a program that fails is an error to type-switch
// on: *scheme.Error for a runtime failure or (error ...), *scheme.ParseError
// for source that does not read, and scheme.ErrIncomplete for source that stops
// short, which a REPL answers by reading another line.
func ExampleError() {
	in := scheme.New()
	ctx := context.Background()

	_, err := in.EvalString(ctx, `(error "no such file:" "notes.txt" 3)`)
	var se *scheme.Error
	if errors.As(err, &se) {
		fmt.Printf("%s | irritants %d\n", se.Message, len(se.Irritants))
	}

	_, err = in.EvalString(ctx, `(+ 1 2`)
	fmt.Println("incomplete:", errors.Is(err, scheme.ErrIncomplete))

	_, err = in.EvalString(ctx, `(+ 1 2))`)
	var pe *scheme.ParseError
	if errors.As(err, &pe) {
		fmt.Printf("parse error at %d:%d\n", pe.Line, pe.Col)
	}
	// Output:
	// no such file: | irritants 2
	// incomplete: true
	// parse error at 1:8
}

// The context bounds an evaluation. A program that never ends, with or
// without calling a primitive, stops when the context is done, and the
// interpreter can be used again.
func ExampleInterp_EvalString_cancel() {
	in := scheme.New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := in.EvalString(ctx, `(let loop () (loop))`)
	fmt.Println(errors.Is(err, context.DeadlineExceeded))
	v, _ := in.EvalString(context.Background(), `(+ 1 2)`)
	fmt.Println(scheme.Write(v))
	// Output:
	// true
	// 3
}

// denyHost is a gate that refuses every primitive declared as touching the
// operating system, which is how an embedder keeps programs from `load`.
type denyHost struct{}

func (denyHost) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if p.Meta.Host {
		return &scheme.Denied{Prim: p.Name, Reason: "this host does not run files"}
	}
	return nil
}

func (denyHost) RegisterChecker(name, verdict string, predicate scheme.Value) error {
	return errors.New("this host has no checkers")
}

// A gate sees every primitive application before its handler runs, builtins
// and prelude procedures included, and refuses by returning an error. A
// refusal is not something the program can catch.
func ExampleInterp_SetGate() {
	in := scheme.New()
	in.SetGate(denyHost{})
	_, err := in.EvalString(context.Background(), `
		(guard (e (#t 'caught))
		  (load "other.scm"))`)
	var d *scheme.Denied
	fmt.Println(errors.As(err, &d), err)
	// Output:
	// true load: denied: this host does not run files
}
