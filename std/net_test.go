package std

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) (srv *httptest.Server, port string) {
	t.Helper()
	srv = httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return srv, u.Port()
}

func netPolicy(port, extra string) string {
	return fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s") (private #t) %s))`, port, extra)
}

func TestHTTPRequestAndHelpers(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Echo-Method", r.Method)
		w.Header().Set("X-Echo-Key", r.Header.Get("X-Api-Key"))
		w.Header().Set("X-Echo-Type", r.Header.Get("Content-Type"))
		w.Header().Set("X-Echo-Agent", r.Header.Get("User-Agent"))
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"got":%q,"n":3}`, string(b))
			return
		}
		w.WriteHeader(404)
		fmt.Fprintf(w, "no:%s:%s", r.URL.Path, b)
	})
	base := srv.URL
	p := netPolicy(port, `(methods get post)`)
	expect(t, p, map[string]string{
		fmt.Sprintf(`(assoc-ref (http-get %q) "status")`, base+"/x"):                                                                                     `404`,
		fmt.Sprintf(`(assoc-ref (http-get %q) "body")`, base+"/x"):                                                                                       `"no:/x:"`,
		fmt.Sprintf(`(assoc-ref (http-post %q "hello") "body")`, base+"/y"):                                                                              `"no:/y:hello"`,
		fmt.Sprintf(`(assoc-ref (assoc-ref (http-get %q '(("x-api-key" . "k1"))) "headers") "x-echo-key")`, base+"/x"):                                   `"k1"`,
		fmt.Sprintf(`(assoc-ref (assoc-ref (http-get %q) "headers") "x-echo-agent")`, base+"/x"):                                                         `"brote"`,
		fmt.Sprintf(`(assoc-ref (http-get-json %q) "n")`, base+"/json"):                                                                                  `3`,
		fmt.Sprintf(`(assoc-ref (http-post-json %q '(("a" . 1))) "n")`, base+"/json"):                                                                    `3`,
		fmt.Sprintf(`(assoc-ref (assoc-ref (http-request "POST" %q '(("content-type" . "application/json")) "[]") "headers") "x-echo-type")`, base+"/x"): `"application/json"`,
		fmt.Sprintf(`(tainted? (http-get %q))`, base+"/x"):                                                                                               `#t`,
		fmt.Sprintf(`(assoc-ref (http-request 'get %q) "url")`, base+"/x"):                                                                               fmt.Sprintf("%q", base+"/x"),
	})
	expectErr(t, p, map[string]string{
		fmt.Sprintf(`(http-request "DELETE" %q)`, base):                 "method DELETE is not allowed",
		fmt.Sprintf(`(http-request "PUT" %q)`, base):                    "method PUT is not allowed",
		fmt.Sprintf(`(http-get %q '(("host" . "evil.example")))`, base): "Host header",
		fmt.Sprintf(`(http-get %q '(1))`, base):                         "headers:",
	})
}

func TestHTTPDefaultMethodsAreReadOnly(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	expectErr(t, netPolicy(port, ""), map[string]string{
		fmt.Sprintf(`(http-post %q "x")`, srv.URL): "method POST is not allowed",
	})
	expect(t, netPolicy(port, ""), map[string]string{
		fmt.Sprintf(`(assoc-ref (http-get %q) "status")`, srv.URL): `200`,
	})
}

func TestHTTPHostAllowlist(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	other, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	expectErr(t, netPolicy(port, ""), map[string]string{
		fmt.Sprintf(`(http-get %q)`, other.URL):                        "not a host",
		`(http-get "http://127.0.0.1/")`:                               "not a host", // right host, wrong port
		`(http-get "https://example.com/")`:                            "not a host",
		fmt.Sprintf(`(http-get "http://user:pw@127.0.0.1:%s/")`, port): "user name or password",
		`(http-get "file:///etc/passwd")`:                              "only http and https",
		`(http-get "ftp://127.0.0.1:21/")`:                             "only http and https",
		`(http-get "http:///x")`:                                       "no host",
		`(http-get "http://exämple.com/")`:                             "not ASCII",
		`(http-get "not a url")`:                                       "",
		fmt.Sprintf(`(http-get "http://127.0.0.1.:%s/")`, port):        "", // trailing dot is the same host: allowed or refused, never a bypass of a different host
	})
	_ = srv
}

func TestAdmits(t *testing.T) {
	for _, c := range []struct {
		pattern, host, port, scheme string
		want                        bool
	}{
		{"example.com", "example.com", "443", "https", true},
		{"example.com", "example.com", "80", "http", true},
		{"example.com", "example.com", "8443", "https", false}, // no port means the default one
		{"example.com", "example.com", "80", "https", false},
		{"example.com:8443", "example.com", "8443", "https", true},
		{"example.com:8443", "example.com", "443", "https", false},
		{"example.com", "evil-example.com", "443", "https", false},
		{"example.com", "example.com.evil.net", "443", "https", false},
		{"*.example.com", "a.example.com", "443", "https", true},
		{"*.example.com", "a.b.example.com", "443", "https", true},
		{"*.example.com", "example.com", "443", "https", false},
		{"*.example.com", "badexample.com", "443", "https", false},
		{"*.example.com:443", "api.example.com", "443", "https", true},
		{"[::1]:8080", "[::1]", "8080", "http", true},
		{"127.0.0.1:8080", "127.0.0.1", "8080", "http", true},
	} {
		if got := admits(c.pattern, c.host, c.port, c.scheme); got != c.want {
			t.Errorf("admits(%q, %q, %q, %q) = %v, want %v", c.pattern, c.host, c.port, c.scheme, got, c.want)
		}
	}
}

func TestPrivateAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "127.1.2.3": true, "10.0.0.1": true, "172.16.0.1": true, "192.168.1.1": true,
		"169.254.169.254": true, "0.0.0.0": true, "100.64.0.1": true, "224.0.0.1": true, "255.255.255.255": true,
		"::1": true, "::": true, "fe80::1": true, "fd00::1": true, "::ffff:127.0.0.1": true, "::ffff:10.1.1.1": true,
		"64:ff9b::7f00:1": true, "2002:7f00:1::": true, "2002:a00:1::": true,
		"8.8.8.8": false, "1.1.1.1": false, "93.184.216.34": false, "2606:4700:4700::1111": false, "64:ff9b::808:808": false,
	} {
		if got := privateAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("privateAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

// A host the policy names is still refused if the address behind it is local,
// unless the policy says (private #t): this is what stops a name that
// resolves to 127.0.0.1 or to a cloud metadata address.
func TestHTTPPrivateAddressesNeedTheirOwnGrant(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "reached") })
	for _, host := range []string{"127.0.0.1", "localhost"} {
		p := fmt.Sprintf(`(policy (net (allow "%s:%s")))`, host, port)
		expectErr(t, p, map[string]string{
			fmt.Sprintf(`(http-get "http://%s:%s/")`, host, port): "private or local",
		})
		p = fmt.Sprintf(`(policy (net (allow "%s:%s") (private #t)))`, host, port)
		expect(t, p, map[string]string{
			fmt.Sprintf(`(assoc-ref (http-get "http://%s:%s/") "body")`, host, port): `"reached"`,
		})
	}
	_ = srv
	expectErr(t, `(policy (net (allow "169.254.169.254")))`, map[string]string{
		`(http-get "http://169.254.169.254/latest/meta-data/")`: "private or local",
	})
}

func TestHTTPRedirects(t *testing.T) {
	var gotKey atomic.Value
	target, tport := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey.Store(r.Header.Get("X-Api-Key"))
		io.WriteString(w, "landed")
	})
	var hops atomic.Int32
	var srv *httptest.Server
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			io.WriteString(w, "final")
		case "/away":
			http.Redirect(w, r, target.URL+"/in", http.StatusFound)
		case "/loop":
			hops.Add(1)
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/elsewhere":
			http.Redirect(w, r, "http://10.255.255.1:9/", http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		}
	})

	p := netPolicy(port, "")
	expect(t, p, map[string]string{
		fmt.Sprintf(`(let ((r (http-get %q))) (list (assoc-ref r "body") (assoc-ref r "url")))`, srv.URL+"/same"): fmt.Sprintf(`("final" %q)`, srv.URL+"/final"),
	})
	expectErr(t, p, map[string]string{
		fmt.Sprintf(`(http-get %q)`, srv.URL+"/away"):      "redirected to",
		fmt.Sprintf(`(http-get %q)`, srv.URL+"/elsewhere"): "redirected to",
		fmt.Sprintf(`(http-get %q)`, srv.URL+"/file"):      "",
		fmt.Sprintf(`(http-get %q)`, srv.URL+"/loop"):      "redirects",
	})
	if n := hops.Load(); n > maxRedirects+1 {
		t.Errorf("followed %d redirects, limit is %d", n, maxRedirects)
	}

	// Both hosts allowed: the redirect is followed, but a header meant for the first host is not sent to the second.
	both := fmt.Sprintf(`(policy (net (allow "127.0.0.1:%s" "127.0.0.1:%s") (private #t)))`, port, tport)
	expect(t, both, map[string]string{
		fmt.Sprintf(`(assoc-ref (http-get %q '(("x-api-key" . "secret"))) "body")`, srv.URL+"/away"): `"landed"`,
	})
	if k := gotKey.Load(); k != "" {
		t.Errorf("the redirect target received X-Api-Key %q", k)
	}
}

func TestHTTPLimits(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			io.WriteString(w, strings.Repeat("x", 2000))
		case "/slow":
			time.Sleep(2 * time.Second)
		default:
			io.WriteString(w, "ok")
		}
	})
	p := netPolicy(port, `(max-body 1KB) (timeout 300ms)`)
	expectErr(t, p, map[string]string{
		fmt.Sprintf(`(http-get %q)`, srv.URL+"/big"):                                      "more than the 1024 bytes",
		fmt.Sprintf(`(http-get %q)`, srv.URL+"/slow"):                                     "",
		fmt.Sprintf(`(http-request "GET" %q '() %q)`, srv.URL, strings.Repeat("y", 2000)): "request body",
	})
	start := time.Now()
	run(t, p, fmt.Sprintf(`(http-get %q)`, srv.URL+"/slow"))
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("the timeout did not stop the request: %v", d)
	}
	expect(t, p, map[string]string{fmt.Sprintf(`(assoc-ref (http-get %q) "body")`, srv.URL+"/ok"): `"ok"`})
}

func TestHTTPAudit(t *testing.T) {
	srv, port := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	var events []Event
	_, err := run(t, netPolicy(port, ""), fmt.Sprintf(`(begin (http-get %q) (http-get "https://example.org/"))`, srv.URL), WithAudit(func(e Event) { events = append(events, e) }))
	if err == nil || len(events) != 2 || events[0].Err != nil || events[1].Err == nil || events[0].Op != "request" || !strings.HasPrefix(events[0].Target, "GET http://127.0.0.1") {
		t.Errorf("err %v, events %+v", err, events)
	}
}
