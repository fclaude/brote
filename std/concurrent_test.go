package std

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

// These run the extended library from many tasks of one interpreter, under
// -race. They are the audit of std for state that is shared between calls: a
// regexp cache, directories opened on first use, the audit callback.

func taskInterp(t *testing.T, p *Policy, opts ...Option) (*scheme.Interp, *Runtime) {
	t.Helper()
	in := scheme.New(scheme.WithTasks(scheme.TaskLimits{MaxTasks: 64}))
	rt, err := Install(in, p, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	return in, rt
}

func evalTasks(t *testing.T, in *scheme.Interp, src string) string {
	t.Helper()
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		t.Fatalf("%s\n  error: %v", src, err)
	}
	return scheme.Write(v)
}

func TestRegexpFromManyTasks(t *testing.T) {
	in, _ := taskInterp(t, Full())
	// more distinct patterns than the cache holds, so it is cleared while
	// other tasks are reading it
	got := evalTasks(t, in, `
(length (pmap (lambda (i)
  (let loop ((n 0) (hits 0))
    (if (= n 100) hits
        (loop (+ n 1)
              (if (regexp-matches? (string-append "^a" (number->string (modulo (+ i n) 200)) "$")
                                   (string-append "a" (number->string (modulo (+ i n) 200))))
                  (+ hits 1) hits)))))
  (iota 16)))`)
	if got != "16" {
		t.Errorf("got %s", got)
	}
}

func TestFilesFromManyTasksAndAuditIsSerialised(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, d := range []string{"data", "out"} {
		os.Mkdir(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "data", "a.txt"), []byte("alpha"), 0o644)
	var events []Event // not safe for concurrent use: std must call the audit one event at a time
	in, _ := taskInterp(t, Allow(FSRead(filepath.Join(root, "data")), FSWrite(filepath.Join(root, "out"))),
		WithBaseDir(root), WithAudit(func(e Event) { events = append(events, e) }))
	got := evalTasks(t, in, `
(pfor-each (lambda (i)
  (fs-write-text (string-append "out/f" (number->string i) ".txt") (fs-read-text "data/a.txt"))
  (fs-exists? "out/f0.txt")
  (length (fs-list "out")))
  (iota 24))
(length (fs-list "out"))`)
	if got != "24" {
		t.Errorf("got %s", got)
	}
	if len(events) != 24 {
		t.Errorf("%d audit events, want 24", len(events))
	}
}

// Whole-file-system grants open a root per volume the first time it is used.
func TestUnscopedFilesFromManyTasks(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644)
	in, _ := taskInterp(t, Full())
	got := evalTasks(t, in, fmt.Sprintf(`(length (pmap (lambda (i) (fs-read-text %q)) (iota 32)))`, filepath.Join(dir, "x")))
	if got != "32" {
		t.Errorf("got %s", got)
	}
}

// The point of the whole feature: requests overlap with no flag anywhere.
func TestRequestsOverlap(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "ok")
	})
	p, err := ParsePolicy(netPolicy(port, ""))
	if err != nil {
		t.Fatal(err)
	}
	in, _ := taskInterp(t, p)
	start := time.Now()
	got := evalTasks(t, in, fmt.Sprintf(`(pmap (lambda (i) (assoc-ref (http-get %q) "body")) (iota 8))`, srv.URL))
	if want := "(" + strings.TrimSpace(strings.Repeat(`"ok" `, 8)) + ")"; got != want {
		t.Errorf("got %s", got)
	}
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Errorf("eight 200ms requests took %v: they did not overlap", d)
	}
}

func TestATimeoutCancelsARequestInFlight(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	})
	p, err := ParsePolicy(netPolicy(port, ""))
	if err != nil {
		t.Fatal(err)
	}
	in, _ := taskInterp(t, p)
	start := time.Now()
	_, err = in.EvalString(context.Background(), fmt.Sprintf(`(with-timeout 0.2 (lambda () (http-get %q)))`, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("the request was not cancelled")
	}
}

func TestSleepAndProgramsStopWithTheirTask(t *testing.T) {
	p := Allow(Module(Time), ExecCommands("sleep"))
	in, _ := taskInterp(t, p)
	start := time.Now()
	// The evaluation ends at once; its tasks, one asleep and one running a
	// program, are cancelled with it rather than waited out.
	evalTasks(t, in, `
(spawn (lambda () (time-sleep 9)))
(spawn (lambda () (proc-run '("sleep" "9"))))
(time-sleep 0.2)
'done`)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("tasks outlived their evaluation (%v)", d)
	}
}
