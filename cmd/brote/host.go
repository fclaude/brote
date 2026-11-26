package main

import (
	"fmt"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// registerHost gives a script what a command-line program needs of the machine
// beyond the language: its own arguments, a file read into a string and a way
// to end the run with a status. (Standard
// input is the interpreter's input, given with scheme.WithInput, so that
// read-line, read-char and read have it.) They are registered here and not in package scheme, which
// has no host: an embedding program decides for itself what its scripts reach
// (docs/embedding.md). Everything else a script can reach comes from package
// std, under the policy the command was given (policy.go); read-file is
// registered only where that policy lets scripts read files, and reads only
// what it lets them read.
func registerHost(in *scheme.Interp, rt *std.Runtime, args []string) error {
	list := make([]scheme.Value, len(args))
	for i, a := range args {
		list[i] = scheme.Str(a)
	}
	prims := []*scheme.Prim{
		{
			Name: "command-line",
			Meta: scheme.Meta{
				Doc:  "The arguments given after -- on the brote command line, as a list of strings (empty when there are none).",
				Host: true,
			},
			Handler: func(c *scheme.Call) (scheme.Value, error) { return scheme.List(list...), nil },
		},
		{
			Name: "exit",
			Meta: scheme.Meta{
				Doc:    "End the run now with an exit status: 0 with no argument or #t, 1 with #f, or the integer given (0 to 255). guard cannot catch it, and an exit in a task ends the whole run.",
				Params: []scheme.Param{{Name: "status", Type: "any", Doc: "an integer from 0 to 255, or #t (0) or #f (1)", Optional: true}},
				Host:   true,
			},
			Handler: func(c *scheme.Call) (scheme.Value, error) {
				code, err := exitCode(c.Args)
				if err != nil {
					return scheme.Value{}, err
				}
				return scheme.Value{}, &scheme.Halt{Err: exitStatus(code)}
			},
		},
	}
	if rt.Policy().Has(std.FS) {
		prims = append(prims, &scheme.Prim{
			Name: "read-file",
			Meta: scheme.Meta{
				Doc:    "The contents of a file, as a string. An unreadable file is an error that names it.",
				Params: []scheme.Param{{Name: "path", Type: "string", Doc: "file to read", Role: "source"}},
				Host:   true,
			},
			Handler: func(c *scheme.Call) (scheme.Value, error) {
				path, err := c.String(0)
				if err != nil {
					return scheme.Value{}, err
				}
				text, err := rt.ReadFile(path)
				if err != nil {
					return scheme.Value{}, err
				}
				return scheme.Str(text), nil
			},
		})
	}
	for _, p := range prims {
		if err := in.Register(p); err != nil {
			return err
		}
	}
	return nil
}

// exitStatus is the stop (exit) raises, inside a scheme.Halt so that guard
// lets it through and a task's exit ends the evaluation; run turns it into
// the command's status.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("the script ended with status %d", int(e)) }

// exitCode is the status (exit) was given, as R7RS reads it: none or #t is
// success, #f is failure, an integer is itself. A status the operating
// system cannot carry is an error rather than one silently cut to a byte.
func exitCode(args []scheme.Value) (int, error) {
	if len(args) == 0 {
		return 0, nil
	}
	v := args[0]
	switch {
	case v.Kind == scheme.KBool && v.IsTrue():
		return 0, nil
	case v.Kind == scheme.KBool:
		return 1, nil
	case v.Kind == scheme.KInt && v.I >= 0 && v.I <= 255:
		return int(v.I), nil
	}
	return 0, fmt.Errorf("argument 1: expected an integer from 0 to 255, #t or #f, got %s", scheme.Write(v))
}

// splitScriptArgs separates the command's own arguments from the script's:
// everything after the first "--" is the script's, and `(command-line)`
// returns it.
func splitScriptArgs(args []string) (own, script []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}
