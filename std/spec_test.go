package std

import (
	"reflect"
	"strings"
	"testing"
)

func TestApplySpec(t *testing.T) {
	p := Allow()
	for _, spec := range []string{
		"regexp", "time", "fs.read=data,/etc/hosts", "fs.write=out", "net=api.x.com,*.y.com:443", "net.methods=get,POST",
		"exec=git,/usr/bin/jq", "exec.env=PATH", "env=HOME", "env.allow=LANG",
	} {
		if err := p.ApplySpec(spec); err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
	}
	want := mustParse(t, `(policy (regexp) (time) (env (allow "HOME" "LANG"))
	  (fs (read "data" "/etc/hosts") (write "out"))
	  (net (allow "api.x.com" "*.y.com:443") (methods get head post))
	  (exec (allow "git" "/usr/bin/jq") (env (allow "PATH"))))`)
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got\n%s\nwant\n%s", p.Pretty(), want.Pretty())
	}
	if err := p.Deny(Net); err != nil || p.Has(Net) || p.Net.Allow != nil {
		t.Errorf("Deny(net): %v, %+v", err, p.Net)
	}
	q := Allow()
	for _, spec := range []string{"fs.read=*", "net=*", "net.private=true", "exec=*", "env=*"} {
		if err := q.ApplySpec(spec); err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
	}
	if !q.FS.AnyRead || q.FS.AnyWrite || !q.Net.Any || !q.Net.Private || !q.Exec.Any || !q.Env.Any {
		t.Errorf("* not applied: %s", q)
	}
}

func TestApplySpecRejects(t *testing.T) {
	for spec, want := range map[string]string{
		"files":             "unknown module",
		"fs":                "needs a value",
		"fs=data":           "has no field",
		"fs.read":           "needs a value",
		"fs.read=":          "needs a value",
		"fs.chmod=x":        "has no field",
		"net":               "needs a value",
		"net=http://x.com":  "not a host pattern",
		"net=a,*":           "cannot be mixed",
		"net.methods=fetch": "unknown method",
		"net.private=maybe": "true or net.private=false",
		"exec=bin/ls":       "absolute path or a bare name",
		"regexp=yes":        "takes no scope",
		"regexp.x":          "takes no scope",
		"env":               "needs a value",
	} {
		err := Allow().ApplySpec(spec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", spec, err, want)
		}
	}
	if err := Allow().Deny("files"); err == nil {
		t.Error("Deny accepted an unknown module")
	}
}

func TestPretty(t *testing.T) {
	got := mustParse(t, `(policy (regexp) (fs (read "/a")))`).Pretty()
	if want := "(policy\n  (regexp)\n  (fs (read \"/a\") (max-file 16777216)))"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := ParsePolicy(got); err != nil {
		t.Errorf("Pretty output does not parse: %v", err)
	}
}
