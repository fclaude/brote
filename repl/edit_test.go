package repl

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// edit feeds keys to an Editor and returns the line it gives back.
func edit(t *testing.T, e *Editor, keys string) (string, error) {
	t.Helper()
	e.r.Reset(strings.NewReader(keys))
	return e.ReadLine("> ")
}

func newTestEditor() *Editor {
	return NewEditor(strings.NewReader(""), io.Discard)
}

func TestEditorKeys(t *testing.T) {
	const (
		left  = "\x1b[D"
		right = "\x1b[C"
		home  = "\x1b[H"
		end   = "\x1b[F"
		del   = "\x1b[3~"
	)
	for _, c := range []struct{ name, keys, want string }{
		{"plain", "(+ 1 2)\r", "(+ 1 2)"},
		{"newline ends a line too", "abc\n", "abc"},
		{"CRLF is one Enter", "abc\r\n", "abc"},
		{"backspace", "abx\x7fc\r", "abc"},
		{"left and insert", "ac" + left + "b\r", "abc"},
		{"home and insert", "bc\x01a\r", "abc"},
		{"ctrl-e", "ab\x01\x05c\r", "abc"},
		{"ctrl-b and ctrl-f", "ac\x02b\x06\r", "abc"},
		{"delete key", "axbc" + home + right + del + "\r", "abc"},
		{"ctrl-d deletes under the cursor", "xabc\x01\x04\r", "abc"},
		{"ctrl-k kills to the end", "abcdef" + left + left + left + "\x0b\r", "abc"},
		{"ctrl-u kills to the start", "abcdef" + left + left + left + "\x15\r", "def"},
		{"ctrl-w kills a word", "(foo bar\x17baz\r", "(foo baz"},
		{"ctrl-y yanks what was killed", "abc\x15\x19\x19\r", "abcabc"},
		{"ctrl-t swaps", "ab\x14\r", "ba"},
		{"alt-b moves a word", "foo bar\x1bbX\r", "foo Xbar"},
		{"end", "ab" + home + end + "c\r", "abc"},
		{"UTF-8", "héé\x7fllo\r", "héllo"},
		{"unknown escape is dropped", "a\x1b[99Zb\r", "ab"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := edit(t, newTestEditor(), c.keys)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestEditorEndsOfInput(t *testing.T) {
	e := newTestEditor()
	if _, err := edit(t, e, "\x04"); err != io.EOF {
		t.Errorf("Ctrl-D on an empty line: %v, want EOF", err)
	}
	if _, err := edit(t, e, "ab\x03"); !errors.Is(err, ErrInterrupt) {
		t.Errorf("Ctrl-C: %v, want ErrInterrupt", err)
	}
	if got, err := edit(t, e, "ab"); err != nil || got != "ab" {
		t.Errorf("input that ends mid-line: %q, %v", got, err)
	}
	if _, err := edit(t, e, ""); err != io.EOF {
		t.Errorf("no input: %v, want EOF", err)
	}
}

func TestEditorHistory(t *testing.T) {
	const up, down = "\x1b[A", "\x1b[B"
	e := newTestEditor()
	e.AddHistory("one")
	e.AddHistory("two")
	e.AddHistory("two") // not twice in a row
	e.AddHistory("   ")
	if got := e.History(); len(got) != 2 {
		t.Fatalf("history = %q", got)
	}
	for _, c := range []struct{ name, keys, want string }{
		{"up", up + "\r", "two"},
		{"up up", up + up + "\r", "one"},
		{"up past the start", up + up + up + "\r", "one"},
		{"up and down", up + up + down + "\r", "two"},
		{"back to what was being typed", "new" + up + up + down + down + "\r", "new"},
		{"edit an old line", up + "!\r", "two!"},
		{"ctrl-p and ctrl-n", "\x10\x10\x0e\r", "two"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := edit(t, e, c.keys)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestEditorHistoryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	e := newTestEditor()
	e.HistoryFile = path
	e.AddHistory("(define x 1)")
	e.AddHistory("(display \"a\nb\")")
	again := newTestEditor()
	again.HistoryFile = path
	again.LoadHistory()
	got := again.History()
	if len(got) != 2 || got[0] != "(define x 1)" || got[1] != "(display \"a\nb\")" {
		t.Fatalf("history = %q", got)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("history file: %v %v, want mode 0600", info, err)
	}
}

func TestEditorCompletion(t *testing.T) {
	names := []string{"string-append", "string-upcase", "string-length", "sort"}
	var out bytes.Buffer
	e := NewEditor(strings.NewReader(""), &out)
	e.Complete = func(line []rune, pos int) (int, []string) {
		start := pos
		for start > 0 && line[start-1] != '(' && line[start-1] != ' ' {
			start--
		}
		var c []string
		for _, n := range names {
			if strings.HasPrefix(n, string(line[start:pos])) {
				c = append(c, n)
			}
		}
		return start, c
	}
	for _, c := range []struct{ name, keys, want string }{
		{"one candidate", "(sor\t\r", "(sort "},
		{"common prefix", "(string-\t\r", "(string-"},
		{"common prefix grows", "(str\t\r", "(string-"},
		{"unique after more typing", "(string-u\t\r", "(string-upcase "},
		{"nothing matches", "(zz\t\r", "(zz"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := edit(t, e, c.keys)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
	out.Reset()
	if _, err := edit(t, e, "(string-\t\t\r"); err != nil {
		t.Fatal(err)
	}
	for _, n := range names[:3] {
		if !strings.Contains(out.String(), n) {
			t.Errorf("second Tab did not list %s:\n%q", n, out.String())
		}
	}
	if strings.Contains(out.String(), "sort") {
		t.Errorf("second Tab listed a name that does not match:\n%q", out.String())
	}
}

func TestEditorScrollsALongLine(t *testing.T) {
	var out bytes.Buffer
	e := NewEditor(strings.NewReader(""), &out)
	e.Width = func() int { return 20 }
	long := strings.Repeat("x", 50)
	if _, err := edit(t, e, long+"\r"); err != nil {
		t.Fatal(err)
	}
	// Every redraw fits in the terminal: the prompt, the text shown and the cursor.
	for _, frame := range strings.Split(out.String(), "\r") {
		frame = strings.TrimSuffix(strings.TrimSuffix(frame, "\x1b[K"), "\n")
		if i := strings.Index(frame, "\x1b["); i >= 0 {
			frame = frame[:i]
		}
		if len([]rune(frame)) > 20 {
			t.Fatalf("frame %q is wider than the terminal", frame)
		}
	}
}
