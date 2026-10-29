package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Net] = installNet }

const maxRedirects = 5

// netw is the http client one interpreter's scripts share. Every request,
// and every redirect it meets, is checked against the policy before a byte
// is sent, and every connection is checked against the address it really
// reaches, whatever name led there.
type netw struct {
	scope     NetScope
	client    *http.Client
	transport *http.Transport
}

func (n *netw) Close() error {
	n.transport.CloseIdleConnections()
	return nil
}

// privateAddr reports whether ip is one a script should not reach by default:
// this machine, its network, link-local (cloud metadata lives here), and
// the ranges reserved for special use.
func privateAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is6() {
		// NAT64 (64:ff9b::/96) and 6to4 (2002::/16) carry an IPv4 address that is the real destination.
		b := ip.As16()
		if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b {
			return privateAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
		if b[0] == 0x20 && b[1] == 0x02 {
			return privateAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range reservedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

var reservedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
		"2001:db8::/32", "100::/64",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// guardDial is the net.Dialer Control hook: it sees the address after name
// resolution, immediately before the connection, so a name that resolves to a
// private address (or changes its answer between the check and the connect) is
// refused here.
func guardDial(private bool) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if private {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("refusing to connect to %q: not an IP address", host)
		}
		if privateAddr(ip) {
			return fmt.Errorf("refusing to connect to %s: a private or local address (the host's net policy has (private #f))", ip)
		}
		return nil
	}
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// target breaks a URL into the pieces the policy judges, or says why it is not
// a URL a script may use at all.
func target(u *url.URL) (host, port string, err error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("only http and https URLs are allowed, got %q", u.Scheme)
	}
	if u.User != nil {
		return "", "", errors.New("a URL with a user name or password is not allowed: send credentials in a header")
	}
	host = strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return "", "", errors.New("the URL has no host")
	}
	for _, r := range host {
		if r > 0x7e || r < 0x21 {
			return "", "", fmt.Errorf("host %q is not ASCII: write an international name in its xn-- form", host)
		}
	}
	port = u.Port()
	if port == "" {
		port = defaultPort(u.Scheme)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host, port, nil
}

// admits reports whether a net allow pattern permits a request to host:port.
func admits(pattern, host, port, scheme string) bool {
	ph, pp, err := splitHostPattern(pattern)
	if err != nil {
		return false
	}
	switch {
	case pp == "" && port != defaultPort(scheme):
		return false
	case pp != "" && pp != port:
		return false
	}
	if ph == host {
		return true
	}
	if suffix, ok := strings.CutPrefix(ph, "*"); ok {
		return strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".")
	}
	return false
}

// allow checks a request URL and method against the policy.
func (n *netw) allow(u *url.URL, method string) error {
	host, port, err := target(u)
	if err != nil {
		return err
	}
	if !n.scope.Any && !slices.ContainsFunc(n.scope.Allow, func(p string) bool { return admits(p, host, port, u.Scheme) }) {
		return fmt.Errorf("%s:%s is not a host this host lets scripts reach", host, port)
	}
	if !slices.Contains(n.scope.Methods, strings.ToLower(method)) {
		return fmt.Errorf("method %s is not allowed (allowed: %s)", strings.ToUpper(method), strings.ToUpper(strings.Join(n.scope.Methods, " ")))
	}
	return nil
}

type header struct{ name, value string }

func (n *netw) do(ctx context.Context, method, rawURL string, hdrs []header, body string, hasBody bool) (scheme.Value, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return scheme.Value{}, fmt.Errorf("bad URL: %w", unwrapURL(err))
	}
	if err := n.allow(u, method); err != nil {
		return scheme.Value{}, err
	}
	if int64(len(body)) > n.scope.MaxBody {
		return scheme.Value{}, fmt.Errorf("the request body is %d bytes, more than the %d this host allows", len(body), n.scope.MaxBody)
	}
	ctx, cancel := context.WithTimeout(ctx, n.scope.Timeout)
	defer cancel()
	var rd io.Reader
	if hasBody {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), u.String(), rd)
	if err != nil {
		return scheme.Value{}, err
	}
	req.Header.Set("User-Agent", "brote")
	for _, h := range hdrs {
		req.Header.Add(h.name, h.value)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return scheme.Value{}, fmt.Errorf("%s %s: %w", strings.ToUpper(method), rawURL, unwrapURL(err))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, n.scope.MaxBody+1))
	if err != nil {
		return scheme.Value{}, fmt.Errorf("%s %s: reading the response: %w", strings.ToUpper(method), rawURL, unwrapURL(err))
	}
	if int64(len(b)) > n.scope.MaxBody {
		return scheme.Value{}, fmt.Errorf("%s %s: the response is more than the %d bytes this host allows", strings.ToUpper(method), rawURL, n.scope.MaxBody)
	}
	names := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		names = append(names, strings.ToLower(k))
	}
	slices.Sort(names)
	var hs []scheme.Value
	for _, k := range slices.Compact(names) {
		hs = append(hs, scheme.Cons(scheme.Str(k), scheme.Str(strings.Join(resp.Header.Values(k), ", "))))
	}
	return scheme.List(
		scheme.Cons(scheme.Str("status"), scheme.Int(int64(resp.StatusCode))),
		scheme.Cons(scheme.Str("headers"), scheme.List(hs...)),
		scheme.Cons(scheme.Str("body"), scheme.Str(strings.ToValidUTF8(string(b), "�"))),
		scheme.Cons(scheme.Str("url"), scheme.Str(resp.Request.URL.String())),
	), nil
}

// unwrapURL removes the "Get \"url\":" prefix net/http puts on its errors, which
// repeats what the caller says.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func installNet(s *installer) error {
	n := &netw{scope: s.pol.Net}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: guardDial(n.scope.Private)}
	n.transport = &http.Transport{
		Proxy:                  nil, // the environment's proxy settings are not the policy's
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  n.scope.Timeout,
		MaxResponseHeaderBytes: 1 << 20,
		MaxIdleConns:           8,
		IdleConnTimeout:        30 * time.Second,
	}
	n.client = &http.Client{
		Transport: n.transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if err := n.allow(req.URL, req.Method); err != nil {
				return fmt.Errorf("redirected to %s: %w", req.URL, err)
			}
			if req.URL.Host != via[0].URL.Host {
				// A header the script set for one host is not for another.
				for k := range req.Header {
					switch http.CanonicalHeaderKey(k) {
					case "User-Agent", "Accept", "Accept-Encoding", "Content-Type":
					default:
						req.Header.Del(k)
					}
				}
			}
			return nil
		},
	}
	s.closers = append(s.closers, n)

	pair := func(v scheme.Value) (string, string, error) {
		if v.Kind != scheme.KPair {
			return "", "", fmt.Errorf("headers: expected an association list of name and value strings, got %s", scheme.Write(v))
		}
		k, ok := atom(v.P.Car)
		val := v.P.Cdr
		if !ok || val.Kind != scheme.KString {
			return "", "", fmt.Errorf("headers: expected (\"name\" . \"value\"), got %s", scheme.Write(v))
		}
		return k, val.S, nil
	}
	s.add(&scheme.Prim{
		Name: "http-request",
		Meta: scheme.Meta{
			Doc: "Send an HTTP request and return the response as an association list with \"status\" (integer), \"headers\" (an alist with lower-case names), \"body\" (a string) and \"url\" (the final URL after redirects). Headers are an alist such as '((\"accept\" . \"text/plain\")). " +
				"A status of 400 or more is a result, not an error; a refused host, method, redirect, a body past the host's limit or a timeout is an error.",
			Params: []scheme.Param{
				{Name: "method", Type: "string", Doc: "GET, POST, ..."},
				{Name: "url", Type: "string", Doc: "http or https URL", Role: "dest"},
				{Name: "headers", Type: "alist", Doc: "request headers", Optional: true},
				{Name: "body", Type: "string", Doc: "request body", Role: "payload", Optional: true},
			},
			Host:        true,
			Egress:      true,
			TaintSource: scheme.TagHTTP,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			method, ok := atom(c.Args[0])
			if !ok || method == "" {
				return scheme.Value{}, fmt.Errorf("argument 1: expected a method such as \"GET\", got %s", scheme.Write(c.Args[0]))
			}
			rawURL, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			var hdrs []header
			if len(c.Args) > 2 {
				items, err := c.List(2)
				if err != nil {
					return scheme.Value{}, err
				}
				for _, it := range items {
					k, v, err := pair(it)
					if err != nil {
						return scheme.Value{}, err
					}
					if strings.EqualFold(k, "host") {
						return scheme.Value{}, errors.New("headers: a Host header cannot be set")
					}
					hdrs = append(hdrs, header{k, v})
				}
			}
			var body string
			hasBody := len(c.Args) > 3
			if hasBody {
				if body, err = c.String(3); err != nil {
					return scheme.Value{}, err
				}
			}
			v, err := n.do(c.Ctx, method, rawURL, hdrs, body, hasBody)
			s.report(Net, "request", strings.ToUpper(method)+" "+rawURL, err)
			return v, err
		},
	})
	return nil
}
