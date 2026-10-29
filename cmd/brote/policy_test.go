package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureDirs(t *testing.T) (root, data, out string) {
	t.Helper()
	root, _ = filepath.EvalSymlinks(t.TempDir())
	data, out = filepath.Join(root, "data"), filepath.Join(root, "out")
	for _, d := range []string{data, out} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, data, "a.txt", "alpha")
	writeFile(t, root, "secret.txt", "s3cret")
	writeFile(t, root, "outside.scm", "(define escaped #t)")
	return
}

func TestDefaultGrantsEverything(t *testing.T) {
	_, data, out := fixtureDirs(t)
	script := `(begin
	  (fs-write-text "` + out + `/o.txt" (string-upcase (fs-read-text "` + data + `/a.txt")))
	  (list (read-file "` + out + `/o.txt")
	        (assoc-ref (proc-run '("echo" "hi")) "stdout")
	        (sha256 "abc")))`
	status, stdout, errs := invoke(t, "", "-e", script)
	if status != 0 || !strings.HasPrefix(stdout, `("ALPHA" "hi\n" "ba78`) {
		t.Fatalf("status %d, stdout %q, stderr %q", status, stdout, errs)
	}
}

func TestSandboxHasNoMachine(t *testing.T) {
	_, data, _ := fixtureDirs(t)
	status, out, _ := invoke(t, "", "-sandbox", "-e", `(sha256 "abc")`)
	if status != 0 || !strings.Contains(out, "ba7816bf") {
		t.Fatalf("pure functions should work in the sandbox: %d %q", status, out)
	}
	for _, expr := range []string{
		`(fs-read-text "` + data + `/a.txt")`, `(read-file "` + data + `/a.txt")`, `(proc-run '("echo"))`,
		`(http-get "http://example.com/")`, `(env-get "HOME")`, `(load "` + data + `/a.txt")`,
	} {
		status, out, errs := invoke(t, "", "-sandbox", "-e", expr)
		if status == 0 || out != "" || errs == "" {
			t.Errorf("%s ran in the sandbox: status %d, stdout %q", expr, status, out)
		}
	}
}

func TestAllowGrantsOnlyWhatIsNamed(t *testing.T) {
	root, data, out := fixtureDirs(t)
	args := []string{"-allow", "fs.read=" + data, "-allow", "fs.write=" + out}
	run := func(expr string) (int, string, string) {
		return invoke(t, "", append(append([]string{}, args...), "-e", expr)...)
	}
	if status, stdout, errs := run(`(begin (fs-write-text "` + out + `/o" "x") (list (read-file "` + data + `/a.txt") (fs-read-text "` + out + `/o")))`); status != 0 || stdout != "(\"alpha\" \"x\")\n" {
		t.Errorf("granted access failed: %d %q %q", status, stdout, errs)
	}
	for _, expr := range []string{
		`(fs-read-text "` + root + `/secret.txt")`, `(fs-read-text "` + data + `/../secret.txt")`, `(fs-write-text "` + data + `/new" "x")`,
		`(fs-write-text "` + root + `/new" "x")`, `(proc-run '("echo"))`, `(http-get "http://example.com/")`, `(load "` + root + `/outside.scm")`,
	} {
		if status, stdout, _ := run(expr); status == 0 || stdout != "" {
			t.Errorf("%s was allowed: %d %q", expr, status, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new")); err == nil {
		t.Error("a refused write created a file")
	}
	// load works inside the grant.
	writeFile(t, data, "lib.scm", "(define from-lib 7)")
	if status, stdout, errs := run(`(begin (load "` + data + `/lib.scm") from-lib)`); status != 0 || stdout != "7\n" {
		t.Errorf("load inside the grant: %d %q %q", status, stdout, errs)
	}
}

func TestDenyRemovesAModule(t *testing.T) {
	status, out, _ := invoke(t, "", "-deny", "exec", "-deny", "net", "-e", `(list (sha256 "") (fs-exists? "/"))`)
	if status != 0 || !strings.HasPrefix(out, `("e3b0`) {
		t.Fatalf("deny left the rest alone: %d %q", status, out)
	}
	for _, expr := range []string{`(proc-run '("echo"))`, `(http-get "http://example.com/")`} {
		if status, _, _ := invoke(t, "", "-deny", "exec", "-deny", "net", "-e", expr); status == 0 {
			t.Errorf("%s ran although its module was denied", expr)
		}
	}
}

func TestPolicyFile(t *testing.T) {
	root, _, _ := fixtureDirs(t)
	pol := writeFile(t, root, "p.policy", `(policy (regexp) (fs (read "data")))`) // data means root/data, the file's own directory
	// Name the file absolutely: the working directory is not the policy file's, but the grant is.
	status, out, errs := invoke(t, "", "-policy", pol, "-e", `(list (regexp-matches? "^a" (read-file "`+filepath.Join(root, "data", "a.txt")+`")))`)
	if status != 0 || out != "(#t)\n" {
		t.Errorf("policy file: %d %q %q", status, out, errs)
	}
	if status, _, _ := invoke(t, "", "-policy", pol, "-e", `(read-file "`+filepath.Join(root, "secret.txt")+`")`); status == 0 {
		t.Error("the policy file's grant was exceeded")
	}
	if status, _, _ := invoke(t, "", "-policy", pol, "-e", `(sha256 "x")`); status == 0 {
		t.Error("a module the policy file does not name was available")
	}
	// -allow adds to the file's grant.
	if status, out, _ := invoke(t, "", "-policy", pol, "-allow", "digest", "-e", `(string-length (sha256 "x"))`); status != 0 || out != "64\n" {
		t.Errorf("-allow on a policy file: %d %q", status, out)
	}
}

func TestPrintPolicy(t *testing.T) {
	status, out, _ := invoke(t, "", "-print-policy", "-allow", "net=api.example.com", "-allow", "regexp")
	if status != 0 || !strings.Contains(out, `(net (allow "api.example.com")`) || !strings.Contains(out, "(regexp)") || strings.Contains(out, "(exec") {
		t.Errorf("%d %q", status, out)
	}
	status, out, _ = invoke(t, "", "-print-policy")
	if status != 0 || !strings.Contains(out, "(exec (allow *)") || !strings.Contains(out, "(fs (read *) (write *)") {
		t.Errorf("default policy: %d %q", status, out)
	}
}

func TestBadPolicyFlags(t *testing.T) {
	root, _, _ := fixtureDirs(t)
	bad := writeFile(t, root, "bad.policy", `(policy (fs))`)
	for name, args := range map[string][]string{
		"unknown module":      {"-allow", "files"},
		"missing value":       {"-allow", "fs"},
		"bad host":            {"-allow", "net=http://x"},
		"unknown deny":        {"-deny", "files"},
		"two starting points": {"-sandbox", "-policy", bad},
		"bad policy file":     {"-policy", bad},
		"missing policy":      {"-policy", filepath.Join(root, "nope")},
	} {
		status, out, errs := invoke(t, "", append(args, "-e", "1")...)
		if status != 2 || out != "" || errs == "" {
			t.Errorf("%s: status %d, stdout %q, stderr %q", name, status, out, errs)
		}
	}
}
