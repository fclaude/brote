package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBroteTestPassing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "math_test.scm", `
(define (double x) (* 2 x))
(test "doubles" (assert-equal 6 (double 3)))
(test "positive" (assert (> (double 1) 0)))
(test "errors" (assert-error (car 5)))
`)
	status, out, errs := invoke(t, "", "test", dir)
	if status != 0 || errs != "" {
		t.Fatalf("status %d, stdout %q, stderr %q", status, out, errs)
	}
	want := "ok    " + filepath.Join(dir, "math_test.scm") + "  (3 tests)\n1 file, 3 tests\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestBroteTestFailureIsReportedWithPositions(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "bad_test.scm", `(define (half x) (/ x 2))
(test "ok" (assert-equal 2 (half 4)))
(test "wrong"
  (assert-equal 3 (half 4)))
(test "raises" (car 5))
(display "printed while testing")
`)
	status, out, _ := invoke(t, "", "test", path)
	if status != 1 {
		t.Fatalf("status %d, stdout %q", status, out)
	}
	for _, want := range []string{
		"FAIL  " + path + "  (3 tests)",
		"FAIL  wrong",
		path + ":4:3: assert-equal failed: (half 4) \"expected\" 3 \"got\" 2",
		"FAIL  raises",
		"car: argument 1: expected pair",
		"output:",
		"printed while testing",
		"3 tests, 2 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ok    ok") {
		t.Errorf("a passing test is listed without -v:\n%s", out)
	}
}

func TestBroteTestVerboseListsPassingTests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a_test.scm", `(test "one" (assert #t))`)
	status, out, _ := invoke(t, "", "test", "-v", dir)
	if status != 0 || !strings.Contains(out, "ok    one") {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

func TestBroteTestFindsFilesInDirectoriesAndSkipsTheRest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a_test.scm", `(test "a" (assert #t))`)
	writeFile(t, dir, "helper.scm", `(error "not a test file")`)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub"), "b_test.scm", `(test "b" (assert #t))`)
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".hidden"), "c_test.scm", `(error "hidden")`)
	status, out, _ := invoke(t, "", "test", dir)
	if status != 0 || !strings.Contains(out, "2 files, 2 tests") {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

func TestBroteTestFilesDoNotShareAnInterpreter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a_test.scm", `(define shared 1) (test "a" (assert-equal 1 shared))`)
	writeFile(t, dir, "b_test.scm", `(test "b" (assert-error shared))`)
	status, out, _ := invoke(t, "", "test", dir)
	if status != 0 {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

func TestBroteTestErrorOutsideATest(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "x_test.scm", "(test \"before\" (assert #t))\n(car 5)\n(test \"after\" (assert #t))\n")
	status, out, _ := invoke(t, "", "test", path)
	if status != 1 || !strings.Contains(out, "error outside a test") || !strings.Contains(out, path+":2:1") || !strings.Contains(out, "could not run to the end") {
		t.Fatalf("status %d, stdout %q", status, out)
	}
	// The test before the error is counted.
	if !strings.Contains(out, "(1 test)") {
		t.Errorf("tests that ran before the error are lost:\n%s", out)
	}
}

func TestBroteTestNothingToRunAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	if status, _, errs := invoke(t, "", "test", dir); status != 1 || !strings.Contains(errs, "no _test.scm files") {
		t.Errorf("empty directory: status %d, stderr %q", status, errs)
	}
	if status, _, errs := invoke(t, "", "test", filepath.Join(dir, "missing")); status != 2 || errs == "" {
		t.Errorf("missing path: status %d, stderr %q", status, errs)
	}
}

func TestBroteTestObeysThePolicy(t *testing.T) {
	dir := t.TempDir()
	secret := writeFile(t, dir, "secret.txt", "hush")
	writeFile(t, dir, "p_test.scm", `(test "reads" (read-file "`+secret+`"))`)
	status, out, _ := invoke(t, "", "-sandbox", "test", filepath.Join(dir, "p_test.scm"))
	if status != 1 || !strings.Contains(out, "FAIL  reads") {
		t.Fatalf("a sandboxed test read a file: status %d, stdout %q", status, out)
	}
	status, out, _ = invoke(t, "", "test", filepath.Join(dir, "p_test.scm"))
	if status != 0 {
		t.Fatalf("status %d, stdout %q", status, out)
	}
}

func TestVIsOnlyForTest(t *testing.T) {
	if status, _, errs := invoke(t, "", "-v", "-e", "1"); status != 2 || !strings.Contains(errs, "test") {
		t.Errorf("status %d, stderr %q", status, errs)
	}
}

func TestTestWord(t *testing.T) {
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"test"}, 0},
		{[]string{"-sandbox", "test", "dir"}, 1},
		{[]string{"-allow", "fs.read=test", "test"}, 2},
		{[]string{"test.scm"}, -1},
		{[]string{"-e", "test"}, -1},
		{[]string{"a.scm", "test"}, -1},
		{nil, -1},
	} {
		if got := testWord(c.args); got != c.want {
			t.Errorf("testWord(%q) = %d, want %d", c.args, got, c.want)
		}
	}
}
