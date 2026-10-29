package std

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

func needUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and coreutils")
	}
}

func TestExecRunsGrantedCommandsWithoutAShell(t *testing.T) {
	needUnix(t)
	dir := t.TempDir()
	p := `(policy (exec (allow "echo" "cat")))`
	expect(t, p, map[string]string{
		`(proc-run '("echo" "hi" "there"))`:                             `(("exit-code" . 0) ("stdout" . "hi there\n") ("stderr" . ""))`,
		`(proc-output '("echo" "x"))`:                                   `"x\n"`,
		`(assoc-ref (proc-run '("cat") '(("stdin" . "fed"))) "stdout")`: `"fed"`,
		`(tainted? (proc-run '("echo")))`:                               `#t`,
	})
	// Nothing in an argument is interpreted.
	evil := "a; touch " + filepath.Join(dir, "PWNED") + " $(touch " + filepath.Join(dir, "PWNED2") + ") | cat > " + filepath.Join(dir, "PWNED3")
	got, err := run(t, p, `(assoc-ref (proc-run (list "echo" `+scheme.Write(scheme.Str(evil))+`)) "stdout")`)
	if err != nil || got != scheme.Write(scheme.Str(evil+"\n")) {
		t.Errorf("got %s, %v", got, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("an argument was run as shell: %v", ents)
	}
}

func TestExecRefusesWhatIsNotGranted(t *testing.T) {
	needUnix(t)
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo")
	}
	expectErr(t, `(policy (exec (allow "echo")))`, map[string]string{
		`(proc-run '("ls"))`:                                     "not a command",
		`(proc-run '("/bin/ls"))`:                                "not a command",
		`(proc-run '("sh" "-c" "echo pwned"))`:                   "not a command",
		`(proc-run '("./echo"))`:                                 "not a command",
		`(proc-run '("echo " "x"))`:                              "not a command", // a space is a different name
		`(proc-run '("ECHO"))`:                                   "not a command",
		`(proc-run '("` + filepath.Dir(echo) + `/../bin/echo"))`: "not a command",
		`(proc-run '())`:                                         "expected a command",
		`(proc-run '(""))`:                                       "expected a command",
		`(proc-run '("echo" 5))`:                                 "expected a string",
		`(proc-run "echo")`:                                      "expected list",
	})
	// Naming the file the allowed name resolves to is the same command; naming a different file is not.
	abs, _ := filepath.Abs(echo)
	expect(t, `(policy (exec (allow "echo")))`, map[string]string{
		`(assoc-ref (proc-run '("` + abs + `" "ok")) "stdout")`: `"ok\n"`,
	})
	expect(t, `(policy (exec (allow "`+abs+`")))`, map[string]string{
		`(assoc-ref (proc-run '("` + abs + `" "ok")) "stdout")`: `"ok\n"`,
	})
	expectErr(t, `(policy (exec (allow "`+abs+`")))`, map[string]string{`(proc-run '("sh"))`: "not a command"})
}

func TestExecAllowedButMissingCommand(t *testing.T) {
	expectErr(t, `(policy (exec (allow "no-such-command-brote-xyz")))`, map[string]string{
		`(proc-run '("no-such-command-brote-xyz"))`: "no-such-command-brote-xyz",
	})
}

func TestExecExitStatusAndEnvironment(t *testing.T) {
	needUnix(t)
	t.Setenv("BROTE_T_X", "x1")
	t.Setenv("BROTE_T_Y", "y1")
	p := `(policy (exec (allow "sh") (env (allow "BROTE_T_X"))))`
	expect(t, p, map[string]string{
		`(assoc-ref (proc-run '("sh" "-c" "exit 3")) "exit-code")`:                          `3`,
		`(assoc-ref (proc-run '("sh" "-c" "echo oops >&2; exit 1")) "stderr")`:              `"oops\n"`,
		`(assoc-ref (proc-run '("sh" "-c" "echo $BROTE_T_X-$BROTE_T_Y-[$HOME]")) "stdout")`: `"x1--[]\n"`,
		`(assoc-ref (proc-run '("sh" "-c" "kill -9 $$")) "exit-code")`:                      `-1`,
	})
	expectErr(t, p, map[string]string{
		`(proc-output '("sh" "-c" "echo bad >&2; exit 2"))`: "exited with status 2: bad",
	})
	if got, err := run(t, `(policy (exec (allow "sh") (env (allow *))))`, `(assoc-ref (proc-run '("sh" "-c" "echo $BROTE_T_Y")) "stdout")`); err != nil || got != `"y1\n"` {
		t.Errorf("(env (allow *)): %s, %v", got, err)
	}
}

func TestExecRunsInTheBaseDirectory(t *testing.T) {
	needUnix(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	got, err := run(t, `(policy (exec (allow "pwd")))`, `(assoc-ref (proc-run '("pwd")) "stdout")`, WithBaseDir(dir))
	if err != nil || !strings.Contains(got, dir) {
		t.Errorf("got %s, %v; want it to name %s", got, err, dir)
	}
}

func TestExecTimeoutAndOutputLimits(t *testing.T) {
	needUnix(t)
	p := `(policy (exec (allow "sh") (timeout 400ms) (max-output 1KB)))`
	start := time.Now()
	_, err := run(t, p, `(proc-run '("sh" "-c" "sleep 5"))`)
	if err == nil || !strings.Contains(err.Error(), "still running") || time.Since(start) > 3*time.Second {
		t.Errorf("timeout: %v after %v", err, time.Since(start))
	}
	// A grandchild that holds the pipes open is stopped with the child.
	start = time.Now()
	_, err = run(t, p, `(proc-run '("sh" "-c" "sleep 5 & wait"))`)
	if err == nil || time.Since(start) > 4*time.Second {
		t.Errorf("process group: %v after %v", err, time.Since(start))
	}
	start = time.Now()
	_, err = run(t, p, `(proc-run '("sh" "-c" "while :; do echo xxxxxxxxxxxxxxxxxxxxxxxx; done"))`)
	if err == nil || !strings.Contains(err.Error(), "more than the 1024 bytes") || time.Since(start) > 3*time.Second {
		t.Errorf("output limit: %v after %v", err, time.Since(start))
	}
	expectErr(t, p, map[string]string{
		`(proc-run '("sh" "-c" "true") '(("timeout" . 60)))`: "more than the",
		`(proc-run '("sh" "-c" "true") '(("timeout" . 0)))`:  "positive",
		`(proc-run '("sh" "-c" "true") '(("cwd" . "/")))`:    "unknown option",
		`(proc-run '("sh" "-c" "true") '(("env" . "x")))`:    "unknown option",
	})
	expect(t, p, map[string]string{
		`(assoc-ref (proc-run '("sh" "-c" "sleep 0.1; echo done") '(("timeout" . 0.3))) "stdout")`: `"done\n"`,
	})
}

func TestExecStopsWithTheContext(t *testing.T) {
	needUnix(t)
	in := scheme.New()
	rt, err := Install(in, mustParse(t, `(policy (exec (allow "sleep") (timeout 1m)))`))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = in.EvalString(ctx, `(proc-run '("sleep" "30"))`)
	if err == nil || time.Since(start) > 5*time.Second || scheme.Catchable(ctx, err) {
		t.Errorf("err %v (catchable %v) after %v", err, err != nil && scheme.Catchable(ctx, err), time.Since(start))
	}
}

func TestExecUnscoped(t *testing.T) {
	needUnix(t)
	expect(t, `(policy (profile full))`, map[string]string{
		`(assoc-ref (proc-run '("echo" "free")) "stdout")`: `"free\n"`,
	})
}

func TestExecAudit(t *testing.T) {
	needUnix(t)
	var events []Event
	_, _ = run(t, `(policy (exec (allow "echo")))`, `(begin (proc-run '("echo" "a" "b")) (proc-run '("ls")))`, WithAudit(func(e Event) { events = append(events, e) }))
	if len(events) != 2 || events[0].Target != "echo a b" || events[0].Err != nil || events[1].Err == nil {
		t.Errorf("events %+v", events)
	}
}
