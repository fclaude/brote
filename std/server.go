package std

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/fclaude/brote/scheme"
)

// serverObj is an HTTP server a script started with serve. It runs on its own
// goroutine until server-stop! or the runtime's Close, and each request's
// handler runs in a fresh root of the interpreter (DetachedContext), under
// the context values of the evaluation that called serve, so a gate and a
// per-caller policy see the caller that started the server.
type serverObj struct {
	in     *scheme.Interp
	ctx    context.Context
	mu     sync.Mutex
	ln     net.Listener
	addr   string
	routes map[string]scheme.Value
	closed bool
}

func (s *serverObj) Kind() string   { return "server" }
func (s *serverObj) String() string { return "#<server " + s.addr + ">" }

// Close stops the server; it is what Runtime.Close calls.
func (s *serverObj) Close() error { return s.stop() }

func (s *serverObj) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

// handle serves one request: look up the route, cap the body, call the Scheme
// handler, and write what it returned.
func (s *serverObj) handle(w http.ResponseWriter, r *http.Request, pol *Policy, report func(string, string, error)) {
	h, ok := s.routes[r.URL.Path]
	if !ok {
		http.Error(w, "no route for "+r.URL.Path, http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, pol.Net.MaxBody+1))
	if err != nil || int64(len(body)) > pol.Net.MaxBody {
		http.Error(w, "the request body is over the host's limit", http.StatusRequestEntityTooLarge)
		return
	}
	req := scheme.List(
		scheme.Cons(scheme.Str("method"), scheme.Str(r.Method)),
		scheme.Cons(scheme.Str("path"), scheme.Str(r.URL.Path)),
		scheme.Cons(scheme.Str("body"), scheme.Str(strings.ToValidUTF8(string(body), "�"))),
	)
	ctx, cancel := context.WithTimeout(s.ctx, pol.Net.Timeout)
	defer cancel()
	v, herr := s.in.Apply(ctx, h, []scheme.Value{req})
	report("handle", r.Method+" "+r.URL.Path, herr)
	if herr != nil {
		http.Error(w, "the handler failed: "+herr.Error(), http.StatusInternalServerError)
		return
	}
	status, out := http.StatusOK, ""
	switch {
	case v.Kind == scheme.KString:
		out = v.S
	default:
		parts, ok := v.Slice()
		if !ok || len(parts) != 2 || parts[0].Kind != scheme.KInt || parts[1].Kind != scheme.KString {
			http.Error(w, "the handler must return a string or (status body)", http.StatusInternalServerError)
			return
		}
		status, out = int(parts[0].I), parts[1].S
	}
	if int64(len(out)) > pol.Net.MaxBody {
		http.Error(w, "the response is over the host's limit", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, out)
}

// installServe registers serve, server-address, server-stop! and server? as
// part of the net module: a server is network I/O, so a policy without the
// net grant leaves serve unbound (absent, not denied), and the bind address is
// checked against the same allow list a request's host is.
func installServe(s *installer) error {
	scope := s.pol.Net

	// bindAllowed reports whether the net grant lets a script bind addr.
	// Loopback exposes nothing outside the machine, so any net grant allows
	// it; any other host must be in the allow list.
	bindAllowed := func(addr string) error {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("serve: %q is not host:port", addr)
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == "" {
			return fmt.Errorf("serve: %q has no host", addr)
		}
		if host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.") {
			return nil
		}
		if scope.Any {
			return nil
		}
		for _, p := range scope.Allow {
			ph, _, err := splitHostPattern(p)
			if err != nil {
				continue
			}
			if ph == host {
				return nil
			}
			if suffix, ok := strings.CutPrefix(ph, "*"); ok && strings.HasSuffix(host, suffix) && host != suffix {
				return nil
			}
		}
		return fmt.Errorf("serve: binding %s is not a host this host lets scripts use (allowed hosts: %s)", host, strings.Join(scope.Allow, ", "))
	}

	serverArg := func(c *scheme.Call) (*serverObj, error) {
		srv, ok := scheme.ObjectOf(c.Args[0]).(*serverObj)
		if !ok {
			return nil, fmt.Errorf("argument 1: expected a server, got %s", scheme.Write(c.Args[0]))
		}
		return srv, nil
	}

	s.add(&scheme.Prim{
		Name: "serve",
		Meta: scheme.Meta{
			Doc: "Start an HTTP server on addr (\"host:port\"; port 0 picks a free one), serving routes, an alist of path to handler procedure. The handler is called with an alist ((method . \"GET\") (path . \"/\") (body . \"…\")) and returns a string (sent with status 200) or a list (status body). Returns a server; see server-address and server-stop!. Loopback addresses are allowed whenever net is granted; any other host must be in the net allow list. Each handler call runs under the context of the evaluation that called serve, so a gate and a per-caller policy see that caller.",
			Params: []scheme.Param{
				{Name: "addr", Type: "string", Doc: "host:port to bind", Role: "dest"},
				{Name: "routes", Type: "alist", Doc: "path to handler procedure"},
			},
			Host: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			addr, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			if err := bindAllowed(addr); err != nil {
				s.report(Net, "serve", addr, err)
				return scheme.Value{}, err
			}
			items, err := c.List(1)
			if err != nil {
				return scheme.Value{}, err
			}
			routes := map[string]scheme.Value{}
			for i, it := range items {
				if it.Kind != scheme.KPair || it.P.Car.Kind != scheme.KString || !it.P.Cdr.IsProcedure() {
					return scheme.Value{}, fmt.Errorf("argument 2: route %d is not (\"path\" . handler), got %s", i+1, scheme.Write(it))
				}
				routes[it.P.Car.S] = it.P.Cdr
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				s.report(Net, "serve", addr, err)
				return scheme.Value{}, fmt.Errorf("serve: %w", err)
			}
			srv := &serverObj{in: c.In, ctx: c.In.DetachedContext(c.Ctx), ln: ln, addr: ln.Addr().String(), routes: routes}
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				srv.handle(w, r, s.pol, func(op, target string, err error) { s.report(Net, op, target, err) })
			})
			go func() { _ = http.Serve(ln, mux) }()
			s.closers = append(s.closers, srv)
			s.report(Net, "serve", srv.addr, nil)
			return scheme.ObjectValue(srv), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "server-address",
		Meta: scheme.Meta{
			Doc:    "The address a server is bound to, as \"host:port\" (with the free port serve picked when it was given 0).",
			Params: []scheme.Param{{Name: "server", Type: "server"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			srv, err := serverArg(c)
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.Str(srv.addr), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "server-stop!",
		Meta: scheme.Meta{
			Doc:    "Stop a server: it closes its listener and answers no more requests. Stopping a stopped server does nothing.",
			Params: []scheme.Param{{Name: "server", Type: "server"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			srv, err := serverArg(c)
			if err != nil {
				return scheme.Value{}, err
			}
			err = srv.stop()
			s.report(Net, "stop", srv.addr, err)
			return scheme.Void, err
		},
	})
	s.add(&scheme.Prim{
		Name: "server?",
		Meta: scheme.Meta{
			Doc:    "True for servers.",
			Params: []scheme.Param{{Name: "x", Type: "any"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			_, ok := scheme.ObjectOf(c.Args[0]).(*serverObj)
			return scheme.Bool(ok), nil
		},
	})
	return nil
}
