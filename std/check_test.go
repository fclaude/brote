package std

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

type callerKey struct{}

// callerOf is the policy of the caller an evaluation runs for, which the host
// put in its context; nil for the host's own evaluations.
func callerOf(ctx context.Context) *Policy {
	p, _ := ctx.Value(callerKey{}).(*Policy)
	return p
}

func asCaller(p *Policy) context.Context {
	return context.WithValue(context.Background(), callerKey{}, p)
}

// callerCheck and callerGate are what an editor writes: the policy comes from
// the caller (here, a *Policy in the context) and the install-wide one is the
// widest any caller may have. callerCheck, given to WithNetCheck, judges every
// step of a request; it refuses with a Denied, as the Gate does.
func callerCheck(ctx context.Context, ev NetEvent) error {
	caller := callerOf(ctx)
	if caller == nil {
		return nil
	}
	if err := caller.CheckNetEvent(ev); err != nil {
		return &scheme.Denied{Prim: "http-request", Reason: err.Error()}
	}
	return nil
}

type callerGate struct{ rt *Runtime }

func (g callerGate) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	caller := callerOf(ctx)
	if caller == nil {
		return nil
	}
	deny := func(err error) error { return &scheme.Denied{Prim: p.Name, Reason: err.Error()} }
	if m := g.rt.ModuleOf(p.Name); m != "" && !caller.Has(m) {
		return deny(fmt.Errorf("%s: this module has not been granted %s", p.Name, m))
	}
	switch p.Name {
	case "proc-run":
		if items, ok := args[0].Slice(); ok && len(items) > 0 {
			if err := caller.CheckExec(items[0].S); err != nil {
				return deny(err)
			}
		}
	}
	return nil
}
func (callerGate) RegisterChecker(name, verdict string, predicate scheme.Value) error { return nil }
func (callerGate) ConcurrentSafe()                                                    {}

// One interpreter, two callers with different grants: one is allowed the
// request, the other is denied it, with a reason a person can read.
func TestAPolicyPerCallerAllowsOneAndDeniesAnother(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello") })
	wide := mustParse(t, fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s" "other.example.com") (methods get post) (private #t)) (exec (allow "echo")))`, port))
	in, rt := taskInterp(t, wide, WithNetCheck(callerCheck))
	in.SetGate(callerGate{rt})

	a := mustParse(t, fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s") (private #t)))`, port))
	b := mustParse(t, `(policy (net (allow "other.example.com")))`)
	c := mustParse(t, `(policy (path))`) // no net at all
	req := fmt.Sprintf(`(assoc-ref (http-get %q) "body")`, srv.URL)

	v, err := in.EvalString(asCaller(a), req)
	if err != nil || scheme.Write(v) != `"hello"` {
		t.Fatalf("caller a: %s, %v", scheme.Write(v), err)
	}
	for name, tc := range map[string]struct {
		ctx  context.Context
		want string
	}{
		"b: another host":    {asCaller(b), "is not a host this module may reach (allowed hosts: other.example.com)"},
		"c: no net module":   {asCaller(c), "has not been granted net"},
		"a: method it lacks": {asCaller(a), "method POST is not allowed for this module (allowed: GET HEAD)"},
		"a: a command":       {asCaller(a), "has not been granted exec"},
	} {
		src := req
		switch name[:1] {
		case "a":
			if strings.Contains(name, "method") {
				src = fmt.Sprintf(`(http-post %q "x")`, srv.URL)
			} else {
				src = `(proc-run '("echo" "hi"))`
			}
		}
		_, err := in.EvalString(tc.ctx, src)
		var d *scheme.Denied
		if !errors.As(err, &d) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a denial containing %q", name, err, tc.want)
		}
	}
	// With no caller policy in the context the install-wide one is all there is.
	if v, err := in.EvalString(context.Background(), req); err != nil || scheme.Write(v) != `"hello"` {
		t.Errorf("no caller: %s %v", scheme.Write(v), err)
	}
}

func TestCheckNetAgreesWithTheInstalledPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy, method, url, want string // want "" is allowed
	}{
		{`(policy (net (allow "api.example.com")))`, "GET", "https://api.example.com/x", ""},
		{`(policy (net (allow "api.example.com")))`, "get", "https://api.example.com:443/x", ""},
		{`(policy (net (allow "api.example.com")))`, "GET", "http://api.example.com/x", ""},
		{`(policy (net (allow "api.example.com")))`, "GET", "https://api.example.com:8443/x", "api.example.com:8443 is not a host"},
		{`(policy (net (allow "api.example.com:8443")))`, "GET", "https://api.example.com:8443/", ""},
		{`(policy (net (allow "api.example.com")))`, "GET", "https://evil.com/", "evil.com:443 is not a host this module may reach (allowed hosts: api.example.com)"},
		{`(policy (net (allow "*.example.com")))`, "HEAD", "https://a.example.com/", ""},
		{`(policy (net (allow "*.example.com")))`, "GET", "https://example.com/", "is not a host"},
		{`(policy (net (allow "api.example.com")))`, "DELETE", "https://api.example.com/", "method DELETE is not allowed for this module (allowed: GET HEAD)"},
		{`(policy (net (allow "api.example.com") (methods get post)))`, "post", "https://api.example.com/", ""},
		{`(policy (net (allow "api.example.com")))`, "GET", "ftp://api.example.com/", "only http and https"},
		{`(policy (net (allow "api.example.com")))`, "GET", "https://u:p@api.example.com/", "user name or password"},
		{`(policy (net (allow "10.0.0.1")))`, "GET", "http://10.0.0.1/", "private or local address"},
		{`(policy (net (allow "10.0.0.1") (private #t)))`, "GET", "http://10.0.0.1/", ""},
		{`(policy (path))`, "GET", "https://api.example.com/", "has not been given network access"},
	} {
		p := mustParse(t, tc.policy)
		err := p.CheckNet(tc.method, tc.url)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s %s %s: %v", tc.policy, tc.method, tc.url, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s %s %s: error %v, want %q", tc.policy, tc.method, tc.url, err, tc.want)
		}
		// What the check allows, the installed policy allows too (the check
		// is the same decision made without sending anything).
		if tc.want == "" {
			if allowErr := (&netw{scope: p.Resolve("").Net}).allow(mustURL(t, tc.url), tc.method); allowErr != nil {
				t.Errorf("CheckNet allowed %s %s but the net module refuses it: %v", tc.method, tc.url, allowErr)
			}
		}
	}
}

func TestCheckExec(t *testing.T) {
	p := mustParse(t, `(policy (exec (allow "echo" "/bin/ls")))`)
	for name, want := range map[string]string{
		"echo":    "",
		"/bin/ls": "",
		"ls":      "ls is not a command this module may run (allowed commands: /bin/ls, echo)",
		"rm":      "rm is not a command",
	} {
		err := p.CheckExec(name)
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	if err := mustParse(t, `(policy (path))`).CheckExec("echo"); err == nil || !strings.Contains(err.Error(), "has not been given permission to run programs") {
		t.Errorf("exec not granted: %v", err)
	}
	if err := mustParse(t, `(policy (exec (allow "echo" "gone-command-xyz")))`).CheckExec("gone-command-xyz"); err == nil || !strings.Contains(err.Error(), "cannot be run here") {
		t.Errorf("unresolvable: %v", err)
	}
}

func TestModuleOfNamesTheInstallingModule(t *testing.T) {
	_, rt := taskInterp(t, mustParse(t, `(policy (net (allow "x.example.com")) (path))`))
	for name, want := range map[string]string{"http-request": "net", "path-join": "path", "require": "", "display": "", "proc-run": ""} {
		if got := rt.ModuleOf(name); got != want {
			t.Errorf("ModuleOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
