package std

import "testing"

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
	})
}
