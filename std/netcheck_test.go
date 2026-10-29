package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fclaude/brote/scheme"
)

// These are the per-caller network checks of WithNetCheck: one interpreter,
// installed with the widest policy any caller has, narrowed for each caller at
// every step of a request, not only the first.

// counted serves body and counts the requests it receives.
func counted(t *testing.T, body string) (url, port string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, body)
	})
	return srv.URL, port, hits
}

// A redirect from a host the caller may reach to one it may not is refused
// before the second host receives anything, though the installed policy
// allows both. The refusal names the host and what the caller may reach,
// guard cannot catch it, and the audit records it.
func TestANetCheckRefusesARedirectOutsideTheCallersGrant(t *testing.T) {
	bURL, bPort, bHits := counted(t, "secret")
	a, aPort := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, bURL+"/data", http.StatusFound)
	})
	wide := mustParse(t, fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s" "127.0.0.1:%s") (private #t)))`, aPort, bPort))
	var audit []Event
	in, rt := taskInterp(t, wide, WithNetCheck(callerCheck), WithAudit(func(e Event) { audit = append(audit, e) }))
	in.SetGate(callerGate{rt})
	onlyA := mustParse(t, fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s") (private #t)))`, aPort))
	notB := fmt.Sprintf("net: 127.0.0.1:%s is not a host this module may reach (allowed hosts: 127.0.0.1:%s)", bPort, aPort)
	get := func(u string) string { return fmt.Sprintf(`(assoc-ref (http-get %q) "body")`, u) }

	for _, src := range []string{
		get(bURL),
		get(a.URL + "/go"),
		fmt.Sprintf(`(guard (e (#t 'caught)) %s)`, get(a.URL+"/go")),
	} {
		v, err := in.EvalString(asCaller(onlyA), src)
		var d *scheme.Denied
		if !errors.As(err, &d) || !strings.Contains(err.Error(), notB) {
			t.Errorf("%s: %s, %v; want a denial containing %q", src, scheme.Write(v), err, notB)
		}
		if strings.Contains(src, "/go") && !strings.Contains(fmt.Sprint(err), "redirected to "+bURL+"/data") {
			t.Errorf("%s: %v; want it to say where the redirect led", src, err)
		}
	}
	if n := bHits.Load(); n != 0 {
		t.Fatalf("the host outside the caller's grant received %d requests", n)
	}
	if len(audit) == 0 {
		t.Fatal("no audit events")
	}
	if last := audit[len(audit)-1]; last.Target != "GET "+a.URL+"/go" || last.Err == nil || !strings.Contains(last.Err.Error(), notB) {
		t.Errorf("the audit has %+v for the refused redirect", last)
	}

	// A caller allowed both hosts follows the redirect.
	if v, err := in.EvalString(asCaller(wide), get(a.URL+"/go")); err != nil || scheme.Write(v) != `"secret"` {
		t.Errorf("a caller allowed both: %s, %v", scheme.Write(v), err)
	}
}

// A name that resolves to a loopback address is judged by the caller's
// (private ...) when the connection is made, on one interpreter, whichever
// caller came first: a connection opened for one caller is not used for
// another's request.
func TestANetCheckJudgesTheAddressANameResolvesToForEachCaller(t *testing.T) {
	_, port, hits := counted(t, "reached")
	grant := func(private string) *Policy {
		return mustParse(t, fmt.Sprintf(`(policy (net (allow "localhost:%s") (private %s)))`, port, private))
	}
	open, closed := grant("#t"), grant("#f")
	in, _ := taskInterp(t, open, WithNetCheck(callerCheck))
	req := fmt.Sprintf(`(assoc-ref (http-get "http://localhost:%s/") "body")`, port)
	for i, caller := range []*Policy{open, closed, open, closed} {
		before := hits.Load()
		v, err := in.EvalString(asCaller(caller), req)
		if caller == open {
			if err != nil || scheme.Write(v) != `"reached"` {
				t.Errorf("request %d, with (private #t): %s, %v", i, scheme.Write(v), err)
			}
			continue
		}
		var d *scheme.Denied
		if !errors.As(err, &d) || !strings.Contains(err.Error(), "net: localhost resolves to ") ||
			!strings.Contains(err.Error(), "a private or local address, which this module may not reach") {
			t.Errorf("request %d, without (private #t): %v", i, err)
		}
		if hits.Load() != before {
			t.Errorf("request %d, without (private #t): the loopback service received it", i)
		}
	}
}

// The check is asked at every step, with the evaluation's context, from tasks
// too: the request, each connection, each redirect hop. A hop that lands on a
// name is judged at connection time by the address the name resolves to.
func TestANetCheckIsAskedAtEveryStepWithTheCallersContext(t *testing.T) {
	_, bPort, bHits := counted(t, "landed")
	toB := fmt.Sprintf("http://localhost:%s/b", bPort)
	a, aPort := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, toB, http.StatusFound) })
	wide := mustParse(t, fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s" "localhost:%s") (private #t)))`, aPort, bPort))
	type step struct {
		caller any
		ev     NetEvent
	}
	var (
		mu         sync.Mutex
		steps      []step
		refuseName atomic.Bool
	)
	in, _ := taskInterp(t, wide, WithNetCheck(func(ctx context.Context, ev NetEvent) error {
		mu.Lock()
		steps = append(steps, step{ctx.Value(callerKey{}), ev})
		mu.Unlock()
		if refuseName.Load() && ev.Stage == NetConnect && ev.Host == "localhost" && ev.Private {
			return errors.New("this caller may not reach local names")
		}
		return nil
	}))
	caller := mustParse(t, `(policy (path))`) // only its identity matters here
	src := fmt.Sprintf(`(pmap (lambda (u) (assoc-ref (http-get u) "body")) (list %q %q))`, a.URL+"/a", a.URL+"/a")
	if v, err := in.EvalString(asCaller(caller), src); err != nil || scheme.Write(v) != `("landed" "landed")` {
		t.Fatalf("%s, %v", scheme.Write(v), err)
	}

	mu.Lock()
	got := steps
	mu.Unlock()
	count := map[string]int{}
	for _, s := range got {
		if s.caller != caller {
			t.Errorf("%+v: the context had %v, not the caller's value", s.ev, s.caller)
		}
		ev := s.ev
		switch ev.Stage {
		case NetRequest:
			count["request"]++
			if ev.Method != "GET" || ev.URL != a.URL+"/a" {
				t.Errorf("request step %+v", ev)
			}
		case NetRedirect:
			count["redirect"]++
			if ev.Method != "GET" || ev.URL != toB {
				t.Errorf("redirect step %+v", ev)
			}
		case NetConnect:
			if !ev.Private || ev.Method != "" || ev.URL != "" {
				t.Errorf("connect step %+v", ev)
			}
			switch {
			case ev.Host == "127.0.0.1" && fmt.Sprint(ev.Addr) == "127.0.0.1:"+aPort:
				count["connect a"]++
			case ev.Host == "localhost" && fmt.Sprint(ev.Addr.Port()) == bPort && ev.Addr.Addr().IsLoopback():
				count["connect b"]++
			default:
				t.Errorf("connect step to an unexpected place: %+v", ev)
			}
		}
	}
	// Two requests: each asked, connected to A, redirected, connected to B
	// (localhost may resolve to ::1 as well as 127.0.0.1, each address a try).
	if count["request"] != 2 || count["redirect"] != 2 || count["connect a"] != 2 || count["connect b"] < 2 {
		t.Errorf("steps %v", count)
	}

	refuseName.Store(true)
	before := bHits.Load()
	if _, err := in.EvalString(asCaller(caller), fmt.Sprintf(`(http-get %q)`, a.URL+"/a")); err == nil || !strings.Contains(err.Error(), "this caller may not reach local names") {
		t.Errorf("a redirect to a local name the check refuses: %v", err)
	}
	if bHits.Load() != before {
		t.Error("the redirect reached the local name the check refused")
	}
}

// A redirect hop is judged with the method it is sent with: a 302 turns a
// POST into a GET, a 307 keeps the POST.
func TestANetCheckJudgesTheMethodOfEachHop(t *testing.T) {
	var landed atomic.Int32
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/302":
			http.Redirect(w, r, "/landing", http.StatusFound)
		case "/307":
			http.Redirect(w, r, "/landing", http.StatusTemporaryRedirect)
		case "/landing":
			landed.Add(1)
			io.WriteString(w, r.Method)
		}
	})
	getAndPost := mustParse(t, netPolicy(port, "(methods get post)"))
	postOnly := mustParse(t, netPolicy(port, "(methods post)"))
	in, _ := taskInterp(t, getAndPost, WithNetCheck(callerCheck))
	post := func(path string) string { return fmt.Sprintf(`(assoc-ref (http-post %q "x") "body")`, srv.URL+path) }

	_, err := in.EvalString(asCaller(postOnly), post("/302"))
	if err == nil || !strings.Contains(err.Error(), "redirected to "+srv.URL+"/landing") ||
		!strings.Contains(err.Error(), "method GET is not allowed for this module (allowed: POST)") {
		t.Errorf("POST, 302, GET for a caller allowed only POST: %v", err)
	}
	if n := landed.Load(); n != 0 {
		t.Errorf("the refused hop was sent (%d)", n)
	}
	for _, tc := range []struct {
		caller     *Policy
		path, want string
	}{
		{getAndPost, "/302", `"GET"`},
		{postOnly, "/307", `"POST"`},
	} {
		if v, err := in.EvalString(asCaller(tc.caller), post(tc.path)); err != nil || scheme.Write(v) != tc.want {
			t.Errorf("POST %s: %s, %v; want %s", tc.path, scheme.Write(v), err, tc.want)
		}
	}
}

// The check can only narrow: one that allows everything does not let a
// request past what the installed policy refuses, at any step.
func TestANetCheckCannotWidenTheInstalledPolicy(t *testing.T) {
	bURL, bPort, bHits := counted(t, "b")
	a, aPort := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, bURL, http.StatusFound) })
	var asked atomic.Int32
	yes := WithNetCheck(func(context.Context, NetEvent) error { asked.Add(1); return nil })
	onlyA := fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s") (private #t)))`, aPort)
	for _, tc := range []struct{ policy, src, want string }{
		{onlyA, fmt.Sprintf(`(http-get %q)`, bURL), "is not a host this host lets scripts reach"},
		{onlyA, fmt.Sprintf(`(http-get %q)`, a.URL), "redirected to " + bURL},
		{onlyA, fmt.Sprintf(`(http-post %q "x")`, a.URL), "method POST is not allowed"},
		{fmt.Sprintf(`(policy (net (allow "localhost:%s")))`, bPort), fmt.Sprintf(`(http-get "http://localhost:%s/")`, bPort), "a private or local address"},
	} {
		if _, err := run(t, tc.policy, tc.src, yes); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s under %s: %v, want an error containing %q", tc.src, tc.policy, err, tc.want)
		}
	}
	if n := bHits.Load(); n != 0 {
		t.Errorf("a host the installed policy refuses received %d requests", n)
	}
	if asked.Load() == 0 {
		t.Error("the check was never asked, so this test shows nothing")
	}
}

func TestCheckAddr(t *testing.T) {
	closed := mustParse(t, `(policy (net (allow "example.com")))`)
	open := mustParse(t, `(policy (net (allow "example.com") (private #t)))`)
	none := mustParse(t, `(policy (path))`)
	for _, tc := range []struct {
		p              *Policy
		host, ip, want string // want "" is allowed
	}{
		{closed, "example.com", "93.184.216.34", ""},
		{closed, "example.com", "127.0.0.1", "net: example.com resolves to 127.0.0.1, a private or local address, which this module may not reach"},
		{closed, "metadata.example.com", "169.254.169.254", "resolves to 169.254.169.254"},
		{closed, "x.example.com", "::ffff:10.0.0.1", "a private or local address"},
		{closed, "127.0.0.1", "127.0.0.1", "net: 127.0.0.1 is a private or local address, which this module may not reach"},
		{closed, "[::1]", "::1", "net: ::1 is a private or local address"},
		{closed, "example.com", "", "no address"},
		{open, "example.com", "127.0.0.1", ""},
		{open, "example.com", "93.184.216.34", ""},
		{none, "example.com", "93.184.216.34", "has not been given network access"},
	} {
		var ip netip.Addr
		if tc.ip != "" {
			ip = netip.MustParseAddr(tc.ip)
		}
		err := tc.p.CheckAddr(tc.host, ip)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s %s %s: %v", tc.p, tc.host, tc.ip, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s %s %s: %v, want %q", tc.p, tc.host, tc.ip, err, tc.want)
		}
	}

	// CheckNetEvent sends each stage to its check, and refuses one it does not know.
	loop := netip.MustParseAddrPort("127.0.0.1:443")
	for ev, want := range map[NetEvent]string{
		{Stage: NetRequest, Method: "GET", URL: "https://example.com/"}:         "",
		{Stage: NetRequest, Method: "POST", URL: "https://example.com/"}:        "method POST is not allowed",
		{Stage: NetRedirect, Method: "GET", URL: "https://evil.example.net/"}:   "evil.example.net:443 is not a host this module may reach",
		{Stage: NetConnect, Host: "example.com", Addr: loop, Private: true}:     "example.com resolves to 127.0.0.1",
		{Stage: "resolve", Host: "example.com", Addr: loop}:                     "unknown stage",
		{Stage: NetConnect, Host: "example.com", Addr: netip.AddrPort{}}:        "no address",
		{Stage: NetConnect, Host: "example.com", Addr: mustAddrPort("1.1.1.1")}: "",
	} {
		err := closed.CheckNetEvent(ev)
		switch {
		case want == "" && err != nil:
			t.Errorf("%+v: %v", ev, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%+v: %v, want %q", ev, err, want)
		}
	}
}

func mustAddrPort(ip string) netip.AddrPort { return netip.AddrPortFrom(netip.MustParseAddr(ip), 443) }
