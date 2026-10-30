package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

func TestFmt(t *testing.T) {
	bad := "(define (f x)\n(+ x 1))\n\n(let ((a 1)\n(b 2))\n(list a b))\n"
	status, out, errs := invoke(t, bad, "fmt")
	if status != 0 {
		t.Fatalf("fmt: status %d, stderr %q", status, errs)
	}
	if out == bad {
		t.Fatal("fmt did not re-indent")
	}
	if !strings.Contains(out, "\n  (+ x 1))") {
		t.Errorf("the body was not indented two spaces:\n%s", out)
	}
	want, err := scheme.Format(bad)
	if err != nil {
		t.Fatal(err)
	}
	if out != want {
		t.Errorf("brote fmt printed %q, scheme.Format gives %q", out, want)
	}
	// Idempotent: formatting the formatted output is byte-identical.
	status, out2, errs := invoke(t, out, "fmt")
	if status != 0 || out2 != out {
		t.Errorf("fmt twice: status %d, out %q, want %q (%s)", status, out2, out, errs)
	}
	// Input the reader rejects is rejected the same way.
	status, _, errs = invoke(t, "(((", "fmt")
	if status != 1 || !strings.Contains(errs, "brote fmt") {
		t.Errorf("bad input: status %d, stderr %q", status, errs)
	}
	// -w rewrites the file in place; without it the file is untouched.
	dir := t.TempDir()
	path := filepath.Join(dir, "a.scm")
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _, errs := invoke(t, "", "fmt", path); status != 0 {
		t.Fatalf("fmt FILE: %s", errs)
	}
	if got, _ := os.ReadFile(path); string(got) != bad {
		t.Errorf("fmt FILE without -w rewrote the file: %q", got)
	}
	if status, _, errs := invoke(t, "", "fmt", "-w", path); status != 0 {
		t.Fatalf("fmt -w: %s", errs)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != out {
		t.Errorf("after -w the file is %q, want %q", got, out)
	}
}
