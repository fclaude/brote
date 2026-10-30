package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/fclaude/brote/scheme"
)

const fmtUsage = `usage: brote fmt [-w] [FILE...]

Format Scheme source the way the reader reads it: each line moves to the
column of the parenthesis it sits in, two spaces deeper than its head, and
trailing whitespace outside strings is dropped. Nothing else changes —
comments, strings and the order of tokens are kept, so the source reads the
same and formatting twice gives the same bytes.

  FILE...   format each file, to standard output
  -w        rewrite each FILE in place
  (no FILE) read standard input, write standard output
`

// runFmt is `brote fmt`: format Scheme source with scheme.Format.
func runFmt(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	write := fs.Bool("w", false, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "brote fmt: %v\n\n%s", err, fmtUsage)
		return 2
	}
	files := fs.Args()
	if len(files) == 0 {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "brote fmt: %v\n", err)
			return 1
		}
		out, err := scheme.Format(string(data))
		if err != nil {
			fmt.Fprintf(stderr, "brote fmt: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, out)
		return 0
	}
	status := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "brote fmt: %v\n", err)
			status = 1
			continue
		}
		out, err := scheme.Format(string(data))
		if err != nil {
			fmt.Fprintf(stderr, "brote fmt: %s: %v\n", path, err)
			status = 1
			continue
		}
		if *write {
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				fmt.Fprintf(stderr, "brote fmt: %v\n", err)
				status = 1
			}
			continue
		}
		fmt.Fprint(stdout, out)
	}
	return status
}

// fmtWord is the position in args of the word "fmt" that makes the command
// format, like testWord: the first argument that is not an option or an
// option's value. -1 if there is none. (To format a file called fmt, write
// ./fmt.)
func fmtWord(args []string) int {
	valued := map[string]bool{"-e": true, "-policy": true, "-allow": true, "-deny": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "fmt":
			return i
		case len(a) > 1 && a[0] == '-':
			if valued[a] {
				i++ // its value
			}
		default:
			return -1
		}
	}
	return -1
}
