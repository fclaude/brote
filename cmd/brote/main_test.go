package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invoke runs the command with the given arguments and standard input.
func invoke(t *testing.T, stdin string, args ...string) (status int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	status = run(args, strings.NewReader(stdin), &out, &errb)
	return status, out.String(), errb.String()
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExpressionPrintsItsValue(t *testing.T) {
	status, out, errs := invoke(t, "", "-e", `(string-append "a" "b")`)
	if status != 0 || out != "\"ab\"\n" || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
}

func TestVoidExpressionPrintsNothing(t *testing.T) {
	status, out, _ := invoke(t, "", "-e", `(define x 1)`)
	if status != 0 || out != "" {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

func TestFilesRunInOneInterpreterInOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.scm", `(define greeting "hello")`)
	b := writeFile(t, dir, "b.scm", `(display greeting) (newline)`)
	status, out, errs := invoke(t, "", a, b)
	if status != 0 || out != "hello\n" || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
}

func TestAnErrorIsOnStderrWithStatusOne(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "bad.scm", `(display "before") (error "boom" 1)`)
	status, out, errs := invoke(t, "", bad)
	if status != 1 || out != "before" || !strings.Contains(errs, "boom 1") || !strings.Contains(errs, "bad.scm") {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
	status, _, errs = invoke(t, "", "-e", `(+ 1`)
	if status != 1 || errs == "" {
		t.Fatalf("an unreadable expression: status %d, stderr %q", status, errs)
	}
}

func TestAMissingFileIsAnError(t *testing.T) {
	status, _, errs := invoke(t, "", filepath.Join(t.TempDir(), "nope.scm"))
	if status != 1 || !strings.Contains(errs, "nope.scm") {
		t.Fatalf("status %d, stderr %q", status, errs)
	}
}

// With nothing to run, brote reads its input as a REPL. A pipe is not a
// terminal, so there are no prompts.
func TestStandardInputIsAREPL(t *testing.T) {
	status, out, _ := invoke(t, "(define (sq x) (* x x))\n(sq 7)\n")
	if status != 0 || out != "49\n" {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

// The one host primitive is load, relative to the working directory.
func TestLoadReadsAFile(t *testing.T) {
	dir := t.TempDir()
	lib := writeFile(t, dir, "lib.scm", `(define (twice x) (* 2 x))`)
	status, out, errs := invoke(t, "", "-e", `(begin (load "`+lib+`") (twice 4))`)
	if status != 0 || out != "8\n" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"-nonsense"}, {"-e", "1", "file.scm"}, {"-e"}} {
		status, _, errs := invoke(t, "", args...)
		if status != 2 || !strings.Contains(errs, "usage: brote") {
			t.Errorf("%v: status %d, stderr %q", args, status, errs)
		}
	}
	if status, out, _ := invoke(t, "", "-h"); status != 0 || !strings.HasPrefix(out, "usage: brote") {
		t.Errorf("-h: status %d, stdout %q", status, out)
	}
}

func TestVersion(t *testing.T) {
	status, out, _ := invoke(t, "", "-version")
	if status != 0 || !strings.HasPrefix(out, "brote ") {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

func TestCommandLineIsWhatFollowsTheDashes(t *testing.T) {
	status, out, errs := invoke(t, "", "-e", `(command-line)`, "--", "a", "b c")
	if status != 0 || out != "(\"a\" \"b c\")\n" || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
	// The script's arguments are not the command's own: a flag after the dashes is data.
	status, out, _ = invoke(t, "", "-e", `(command-line)`, "--", "-version")
	if status != 0 || out != "(\"-version\")\n" {
		t.Fatalf("status %d, stdout %q", status, out)
	}
	status, out, _ = invoke(t, "", "-e", `(command-line)`)
	if status != 0 || out != "()\n" {
		t.Fatalf("no arguments: status %d, stdout %q", status, out)
	}
}

func TestReadFileGivesTheContentsAndNamesAFileItCannotRead(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "in.txt", "line one\nline two\n")
	status, out, errs := invoke(t, "", "-e", `(read-file "`+p+`")`)
	if status != 0 || out != "\"line one\\nline two\\n\"\n" || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
	status, _, errs = invoke(t, "", "-e", `(read-file "`+filepath.Join(dir, "missing")+`")`)
	if status != 1 || !strings.Contains(errs, "missing") || strings.Count(errs, "read-file") > 1 {
		t.Fatalf("status %d, stderr %q", status, errs)
	}
}

func TestReadLineTakesStandardInputLineByLineThenEOF(t *testing.T) {
	status, out, errs := invoke(t, "one\ntwo\r\nthree", "-e", `(list (read-line) (read-line) (read-line) (read-line))`)
	if status != 0 || out != "(\"one\" \"two\" \"three\" #<eof>)\n" || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
	status, out, _ = invoke(t, "", "-e", `(eof-object? (read-line))`)
	if status != 0 || out != "#t\n" {
		t.Fatalf("empty input: status %d, stdout %q", status, out)
	}
}

func TestStandardInputIsReadableAsCharactersAndData(t *testing.T) {
	status, out, errs := invoke(t, "ab (1 \"two\" three) rest", "-e", `(list (read-char) (peek-char) (read-char) (read) (read-string 3))`)
	if status != 0 || out != "(#\\a #\\b #\\b (1 \"two\" three) \" re\")\n" || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
}

// The REPL reads standard input itself, so a script's read-line would take
// lines from under it: in the REPL the current input has nothing in it.
func TestTheReplHasNoStandardInputToRead(t *testing.T) {
	status, out, errs := invoke(t, "(command-line)\n(eof-object? (read-line))\n", "--", "x")
	if status != 0 || !strings.Contains(out, `("x")`) || !strings.Contains(out, "#t") || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
}
