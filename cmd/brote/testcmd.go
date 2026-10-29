package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// testSuffix names the files a directory given to `brote test` is searched for.
const testSuffix = "_test.scm"

// testResult is what one test did.
type testResult struct {
	name    string
	passed  bool
	message string
	where   string
	trace   []string
}

// fileResult is what running one file of tests came to: the tests it ran, and
// the error that stopped the file before or between them, if any.
type fileResult struct {
	path   string
	tests  []testResult
	err    error  // loading the file failed outside a test
	output string // what the file printed
}

func (f *fileResult) failed() int {
	n := 0
	for _, t := range f.tests {
		if !t.passed {
			n++
		}
	}
	return n
}

func (f *fileResult) ok() bool { return f.err == nil && f.failed() == 0 }

// testFiles lists the files to run: each path that is a file, and every file
// ending in _test.scm under each path that is a directory, in order.
func testFiles(paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		var found []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != p && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), testSuffix) {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(found)
		files = append(files, found...)
	}
	return files, nil
}

// runTests is `brote test`: every file in an interpreter of its own, under the
// policy the command was given, and a report. The status is 0 if everything
// passed, 1 if a test failed or a file could not be run, and 2 for a path that
// cannot be read.
func runTests(paths []string, pol *std.Policy, restricted, verbose bool, stdout, stderr io.Writer) int {
	files, err := testFiles(paths)
	if err != nil {
		fmt.Fprintf(stderr, "brote test: %v\n", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintf(stderr, "brote test: no %s files in %s\n", testSuffix, strings.Join(append([]string{}, orDot(paths)...), ", "))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var results []*fileResult
	for _, f := range files {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "brote test: interrupted")
			return 1
		}
		results = append(results, runTestFile(ctx, f, pol, restricted))
	}
	return report(results, verbose, stdout)
}

func orDot(paths []string) []string {
	if len(paths) == 0 {
		return []string{"."}
	}
	return paths
}

// runTestFile loads one file in a new interpreter and collects its tests.
func runTestFile(ctx context.Context, path string, pol *std.Policy, restricted bool) *fileResult {
	res := &fileResult{path: path}
	var out bytes.Buffer
	in := scheme.New(scheme.WithOutput(&out), scheme.WithTasks(scheme.TaskLimits{}))
	rt, err := std.Install(in, pol)
	if err != nil {
		res.err = err
		return res
	}
	defer rt.Close()
	if restricted {
		in.SetGate(rt.Gate(nil))
	}
	if err := registerHost(in, rt, nil); err != nil {
		res.err = err
		return res
	}
	res.err = in.LoadFile(ctx, path)
	res.output = out.String()
	// The tests that ran before an error outside one still count.
	v, err := in.EvalString(ctx, "(test-results)")
	if err != nil {
		if res.err == nil {
			res.err = err
		}
		return res
	}
	items, _ := v.Slice()
	for _, it := range items {
		parts, ok := it.Slice()
		if !ok || len(parts) != 5 {
			continue
		}
		t := testResult{name: scheme.Display(parts[0]), passed: parts[1].IsTrue(), message: scheme.Display(parts[2])}
		if parts[3].Kind == scheme.KString {
			t.where = parts[3].S
		}
		frames, _ := parts[4].Slice()
		for _, f := range frames {
			t.trace = append(t.trace, scheme.Display(f))
		}
		// The innermost frame is where the test failed, which is already said.
		if len(t.trace) > 0 && t.where != "" && strings.HasSuffix(t.trace[0], "("+t.where+")") {
			t.trace = t.trace[1:]
		}
		res.tests = append(res.tests, t)
	}
	return res
}

// report writes what happened and returns the status.
func report(results []*fileResult, verbose bool, w io.Writer) int {
	files, tests, failedTests, failedFiles := len(results), 0, 0, 0
	for _, r := range results {
		tests += len(r.tests)
		failedTests += r.failed()
		if !r.ok() {
			failedFiles++
		}
		label := "ok  "
		if !r.ok() {
			label = "FAIL"
		}
		fmt.Fprintf(w, "%s  %s  (%s)\n", label, r.path, plural(len(r.tests), "test"))
		for _, t := range r.tests {
			if t.passed && !verbose {
				continue
			}
			if t.passed {
				fmt.Fprintf(w, "      ok    %s\n", t.name)
				continue
			}
			fmt.Fprintf(w, "      FAIL  %s\n", t.name)
			if t.where != "" {
				fmt.Fprintf(w, "            %s: %s\n", t.where, t.message)
			} else {
				fmt.Fprintf(w, "            %s\n", t.message)
			}
			for _, fr := range t.trace {
				fmt.Fprintf(w, "              %s\n", fr)
			}
		}
		if r.err != nil {
			fmt.Fprintf(w, "      error outside a test: %s\n", strings.ReplaceAll(scheme.FormatError(r.err), "\n", "\n      "))
		}
		if (!r.ok() || verbose) && strings.TrimSpace(r.output) != "" {
			fmt.Fprintf(w, "      output:\n")
			for _, line := range strings.Split(strings.TrimRight(r.output, "\n"), "\n") {
				fmt.Fprintf(w, "        %s\n", line)
			}
		}
	}
	summary := fmt.Sprintf("%s, %s", plural(files, "file"), plural(tests, "test"))
	if failedTests > 0 {
		summary += fmt.Sprintf(", %d failed", failedTests)
	}
	if failedFiles > failedFilesWithTests(results) {
		summary += fmt.Sprintf(", %s could not run to the end", plural(failedFiles-failedFilesWithTests(results), "file"))
	}
	fmt.Fprintln(w, summary)
	if failedFiles > 0 {
		return 1
	}
	return 0
}

// failedFilesWithTests counts the files that failed because a test did.
func failedFilesWithTests(results []*fileResult) int {
	n := 0
	for _, r := range results {
		if r.failed() > 0 {
			n++
		}
	}
	return n
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
