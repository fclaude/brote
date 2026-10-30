package std

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

func TestServe(t *testing.T) {
	pol := `(policy (net (allow *) (private #t)))`
	expect(t, pol, map[string]string{
		// A route's handler answers, and serve returns a server whose
		// address names the free port it bound.
		`(define srv (serve "127.0.0.1:0" (list (cons "/" (lambda (req) "hello from brote")))))
		 (define resp (http-request "GET" (string-append "http://" (server-address srv) "/")))
		 (list (assoc-ref resp "status") (assoc-ref resp "body") (server? srv))`: `(200 "hello from brote" #t)`,
		// The handler sees the request's method and path.
		`(define srv (serve "127.0.0.1:0" (list (cons "/" (lambda (req) (string-append (assoc-ref req "method") " " (assoc-ref req "path")))))))
		 (assoc-ref (http-request "GET" (string-append "http://" (server-address srv) "/")) "body")`: `"GET /"`,
		// A handler may return (status body).
		`(define srv (serve "127.0.0.1:0" (list (cons "/" (lambda (req) (list 201 "made"))))))
		 (define resp (http-request "GET" (string-append "http://" (server-address srv) "/")))
		 (list (assoc-ref resp "status") (assoc-ref resp "body"))`: `(201 "made")`,
		// A path with no route is a 404, a result not an error.
		`(define srv (serve "127.0.0.1:0" (list (cons "/" (lambda (req) "ok")))))
		 (assoc-ref (http-request "GET" (string-append "http://" (server-address srv) "/nope")) "status")`: `404`,
	})
	expectErr(t, pol, map[string]string{
		// A stopped server answers nothing.
		`(define srv (serve "127.0.0.1:0" (list (cons "/" (lambda (req) "ok")))))
		 (server-stop! srv)
		 (http-request "GET" (string-append "http://" (server-address srv) "/"))`: "refused",
		// A route that is not (path . handler) is refused.
		`(serve "127.0.0.1:0" (list (cons "/" 5)))`: "route 1",
	})

	// Fail closed: without the net grant serve does not exist.
	expectErr(t, `(policy (profile safe))`, map[string]string{
		`(serve "127.0.0.1:0" '())`: "unbound variable: serve",
	})

	// The bind address is scoped like a request's host: loopback is allowed
	// under any net grant, anything else must be in the allow list.
	expect(t, `(policy (net (allow "example.com")))`, map[string]string{
		`(server? (serve "127.0.0.1:0" '()))`: `#t`,
	})
	expectErr(t, `(policy (net (allow "example.com")))`, map[string]string{
		`(serve "0.0.0.0:0" '())`: "not a host",
		// Loopback is judged on the address, not on how a name starts.
		`(serve "127.example.invalid:0" '())`: "not a host",
	})
	expect(t, `(policy (net (allow "example.com")))`, map[string]string{
		`(server? (serve "[::1]:0" '()))`: `#t`,
	})
	// A status outside what HTTP can carry is the handler's error, a 500.
	expect(t, pol, map[string]string{
		`(define srv (serve "127.0.0.1:0" (list (cons "/" (lambda (req) (list 5 "x"))))))
		 (assoc-ref (http-request "GET" (string-append "http://" (server-address srv) "/")) "status")`: `500`,
	})
	// Errors carry the procedure's name once.
	_, err := run(t, pol, `(serve "nohost" '())`)
	if err == nil || strings.Count(err.Error(), "serve:") != 1 {
		t.Errorf("serve error: %v", err)
	}
}

// The host's WithNetCheck is asked before serve binds, with the caller's
// context, so a narrower caller cannot bind what the installed policy allows.
func TestServeNetCheck(t *testing.T) {
	var got []NetEvent
	refuse := errors.New("this caller may not serve")
	check := WithNetCheck(func(ctx context.Context, ev NetEvent) error {
		got = append(got, ev)
		return refuse
	})
	_, err := run(t, `(policy (net (allow *)))`, `(serve "LOCALHOST:0" '())`, check)
	if err == nil || !strings.Contains(err.Error(), refuse.Error()) {
		t.Errorf("serve under a refusing check: %v", err)
	}
	if len(got) != 1 || got[0].Stage != NetListen || got[0].Host != "localhost" {
		t.Errorf("check saw %+v, want one NetListen for localhost", got)
	}
	// CheckNetEvent judges a bind with CheckListen.
	narrow := mustParse(t, `(policy (net (allow "example.com")))`)
	if err := narrow.CheckNetEvent(NetEvent{Stage: NetListen, Host: "0.0.0.0"}); err == nil {
		t.Error("CheckListen allowed 0.0.0.0 for a policy that only names example.com")
	}
	if err := narrow.CheckNetEvent(NetEvent{Stage: NetListen, Host: "127.0.0.1"}); err != nil {
		t.Errorf("CheckListen refused loopback: %v", err)
	}
}

// Runtime.Close stops the servers scripts started, and serve after Close
// fails rather than starting one nothing will stop.
func TestServeStoppedByClose(t *testing.T) {
	in := scheme.New()
	rt, err := Install(in, mustParse(t, `(policy (net (allow *)))`))
	if err != nil {
		t.Fatal(err)
	}
	v, err := in.EvalString(context.Background(), `(server-address (serve "127.0.0.1:0" '()))`)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", v.S, time.Second); err == nil {
		c.Close()
		t.Errorf("the server at %s still accepts connections after Runtime.Close", v.S)
	}
	if _, err := in.EvalString(context.Background(), `(serve "127.0.0.1:0" '())`); err == nil {
		t.Error("serve after Runtime.Close succeeded")
	}
}
