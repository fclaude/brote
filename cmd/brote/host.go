package main

import (
	"bufio"
	"errors"
	"io"
	"strings"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// registerHost gives a script what a command-line program needs of the machine
// beyond the language: its own arguments, a file read into a string, and a line
// of standard input. They are registered here and not in package scheme, which
// has no host: an embedding program decides for itself what its scripts reach
// (docs/embedding.md). Everything else a script can reach comes from package
// std, under the policy the command was given (policy.go); read-file is
// registered only where that policy lets scripts read files, and reads only
// what it lets them read.
//
// readLine is registered only when the command does not read standard input
// itself: the REPL does, and a script's read-line would take lines from under it.
func registerHost(in *scheme.Interp, rt *std.Runtime, args []string, stdin io.Reader, readLine bool) error {
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
	if readLine {
		r := bufio.NewReader(stdin)
		prims = append(prims, &scheme.Prim{
			Name: "read-line",
			Meta: scheme.Meta{
				Doc:  "The next line of standard input without its line ending, or #f once there is no more.",
				Host: true,
			},
			Handler: func(c *scheme.Call) (scheme.Value, error) {
				line, err := r.ReadString('\n')
				if err != nil && (!errors.Is(err, io.EOF) || line == "") {
					if errors.Is(err, io.EOF) {
						return scheme.Bool(false), nil
					}
					return scheme.Value{}, err
				}
				return scheme.Str(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")), nil
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
