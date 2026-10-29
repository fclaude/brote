package std

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

const sample = `(policy
  (profile safe)
  (env (allow "HOME" "LANG"))
  (fs (read "data") (write "out") (max-file 4MB))
  (net (allow "api.example.com" "*.github.com:443") (methods get post)
       (max-body 1MB) (timeout 10s))
  (exec (allow "git" "/usr/bin/jq") (timeout 30s) (max-output 1MB) (env (allow "PATH")))
  (time (max-sleep 2s)))`

func mustParse(t testing.TB, src string) *Policy {
	t.Helper()
	p, err := ParsePolicy(src)
	if err != nil {
		t.Fatalf("ParsePolicy(%s): %v", src, err)
	}
	return p
}

func TestParseSample(t *testing.T) {
	p := mustParse(t, sample)
	want := []string{Path, Encoding, Regexp, Digest, Time, Rand, Env, FS, Net, Exec}
	if got := p.Granted(); !reflect.DeepEqual(got, want) {
		t.Fatalf("granted %v, want %v", got, want)
	}
	if p.FS.MaxFile != 4*mib || p.Net.MaxBody != mib || p.Time.MaxSleep.Seconds() != 2 {
		t.Errorf("limits not read: %+v %+v %+v", p.FS, p.Net, p.Time)
	}
	if !reflect.DeepEqual(p.Net.Methods, []string{"get", "post"}) {
		t.Errorf("methods = %v", p.Net.Methods)
	}
}

func TestPolicyRoundTrips(t *testing.T) {
	for _, src := range []string{
		sample, `(policy)`, `(policy (profile pure))`, `(policy (profile full))`,
		`(policy (regexp) (fs (read *)))`, `(policy (net (allow *) (private #t)))`,
		`(policy (profile full) (fs (read "x")))`,
	} {
		p := mustParse(t, src)
		q, err := ParsePolicy(p.String())
		if err != nil {
			t.Errorf("%s: printed form %s does not parse: %v", src, p, err)
			continue
		}
		if !reflect.DeepEqual(p, q) {
			t.Errorf("%s: round trip changed the policy:\n%s\n%s", src, p, q)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for src, want := range map[string]string{
		`(polcy)`:                                    "expected a (policy",
		`(policy) (policy)`:                          "one (policy",
		`(policy (profile huge))`:                    "unknown profile",
		`(policy (flies))`:                           "unknown clause",
		`(policy (fs (read "a")) (fs))`:              "twice",
		`(policy (fs))`:                              "needs (read",
		`(policy (fs (read "a") (mode 1)))`:          "unknown field",
		`(policy (env))`:                             "needs (allow",
		`(policy (net))`:                             "needs (allow",
		`(policy (net (allow "http://x.com")))`:      "not a host pattern",
		`(policy (net (allow "a.*.com")))`:           "not a host pattern",
		`(policy (net (allow "x" *)))`:               "cannot be mixed",
		`(policy (net (allow "x") (methods fetch)))`: "unknown method",
		`(policy (net (allow "x") (private 1)))`:     "#t or #f",
		`(policy (exec))`:                            "needs (allow",
		`(policy (exec (allow "bin/ls")))`:           "absolute path or a bare name",
		`(policy (regexp (x)))`:                      "takes no fields",
		`(policy (fs (read "a") (max-file 0)))`:      "size",
		`(policy (net (allow "x") (timeout -1)))`:    "duration",
		`(policy 5)`:                                 "starts with a name",
		`(policy (fs (read "a")`:                     "std: policy",
	} {
		_, err := ParsePolicy(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", src, err, want)
		}
	}
}

func TestSectionOverridesProfile(t *testing.T) {
	p := mustParse(t, `(policy (profile full) (fs (read "x")))`)
	if p.FS.AnyRead || p.FS.AnyWrite || len(p.FS.Read) != 1 {
		t.Errorf("an (fs ...) clause should replace the profile's unscoped fs: %+v", p.FS)
	}
	if !p.Net.Any || !p.Exec.Any {
		t.Error("other modules of the profile should stay unscoped")
	}
}

func eval(t *testing.T, in *scheme.Interp, src string) (scheme.Value, error) {
	t.Helper()
	return in.EvalString(context.Background(), src)
}

func TestUngrantedIsAbsent(t *testing.T) {
	in := scheme.New()
	rt, err := Install(in, mustParse(t, `(policy (profile pure))`))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if v, err := eval(t, in, `(path-join "a" "b")`); err != nil || v.S != "a/b" {
		t.Fatalf("path-join = %v, %v", v, err)
	}
	for _, name := range []string{"fs-read-text", "http-request", "proc-run", "env-get", "time-now"} {
		if _, ok := in.Lookup(name); ok {
			t.Errorf("%s is registered under (profile pure)", name)
		}
		if _, err := eval(t, in, "("+name+")"); err == nil || !strings.Contains(err.Error(), "unbound") {
			t.Errorf("(%s) = %v, want an unbound-variable error", name, err)
		}
	}
	if _, err := eval(t, in, `(require 'fs)`); err == nil || !strings.Contains(err.Error(), "fs is not granted") {
		t.Errorf("require of an ungranted module: %v", err)
	}
	if v, err := eval(t, in, `(require 'path 'regexp)`); err != nil || !v.IsTrue() {
		t.Errorf("require of granted modules: %v, %v", v, err)
	}
	if v, _ := eval(t, in, `(module-info 'fs)`); v.IsTrue() {
		t.Errorf("module-info of an ungranted module = %v, want #f", v)
	}
	if v, _ := eval(t, in, `(length (modules))`); v.I != 4 {
		t.Errorf("(modules) has %d entries, want 4", v.I)
	}
}

func TestNothingGrantedNothingInstalled(t *testing.T) {
	in := scheme.New()
	before := len(in.Prims())
	rt, err := Install(in, &Policy{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var added []string
	for _, p := range in.Prims()[:0] {
		added = append(added, p.Name)
	}
	if got := len(in.Prims()) - before; got != 3 {
		t.Errorf("an empty policy installed %d procedures, want 3 (require, modules, module-info)", got)
	}
	_ = added
}

func TestInstallRejectsEmptyScope(t *testing.T) {
	for name, p := range map[string]*Policy{
		"fs":   {Modules: map[string]bool{FS: true}},
		"net":  {Modules: map[string]bool{Net: true}},
		"exec": {Modules: map[string]bool{Exec: true}},
		"env":  {Modules: map[string]bool{Env: true}},
		"junk": {Modules: map[string]bool{"files": true}},
	} {
		if rt, err := Install(scheme.New(), p); err == nil {
			rt.Close()
			t.Errorf("%s: Install accepted a grant with nothing to reach", name)
		}
	}
}

func TestModuleInfoIsPolicySyntax(t *testing.T) {
	in := scheme.New()
	rt, err := Install(in, mustParse(t, `(policy (fs (read "/data") (max-file 1MB)))`))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	v, err := eval(t, in, `(module-info 'fs)`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := scheme.Write(v), `((read "/data") (max-file 1048576))`; got != want {
		t.Errorf("module-info = %s, want %s", got, want)
	}
}

func TestInstallTwiceFails(t *testing.T) {
	in := scheme.New()
	rt, err := Install(in, mustParse(t, `(policy (profile pure))`))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if _, err := Install(in, mustParse(t, `(policy (profile pure))`)); err == nil {
		t.Error("a second Install on the same interpreter should fail, not shadow")
	}
}

func FuzzParsePolicy(f *testing.F) {
	f.Add(sample)
	f.Add(`(policy (profile full))`)
	f.Add(`(policy (fs (read * )))`)
	f.Fuzz(func(t *testing.T, src string) {
		p, err := ParsePolicy(src)
		if err != nil {
			return
		}
		q, err := ParsePolicy(p.String())
		if err != nil {
			t.Fatalf("printed policy does not parse: %s: %v", p, err)
		}
		if !reflect.DeepEqual(p, q) {
			t.Fatalf("round trip changed the policy:\n%s\n%s", p, q)
		}
	})
}
