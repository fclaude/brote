package std

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// Install enforces one Policy inside the procedures it registers. A host that
// runs code for several callers on one interpreter (an editor whose modules
// each declare what they need, say) installs the widest policy any caller may
// have and narrows it per call with the calling module's own Policy. The
// network is narrowed by WithNetCheck, whose function asks CheckNetEvent at
// every step of a request: the request, each redirect hop and each
// connection. A Gate sees only http-request's arguments, so it cannot. The
// rest is narrowed in the Gate: ModuleOf and Has for the module, CheckExec for
// proc-run's command. The messages are written to be shown to a person.
//
// The installed policy stays the outer limit and is still enforced inside the
// procedures, so a Check that is too permissive, or a call nothing asked
// about, cannot reach further than the installed policy allows.
// CheckFS is not offered: the file system's containment is the operating
// system's (os.Root, which also refuses a symbolic link out of the tree), and a
// check that only reads the path would let a link inside one module's tree
// lead into another's. A host that needs different trees for different callers
// installs one interpreter per distinct file grant.

// CheckNet reports why a script holding p may not send an HTTP request with
// this method to rawURL, or nil if it may. It judges what Install's net
// module judges before a byte is sent, and again before each redirect hop:
// that net is granted, the scheme, the host and port against the allow list,
// and the method. A host named by an IP address is also refused when it is
// private or local and p does not have (private #t). A host name that
// resolves to such an address is judged when the connection is made, by
// CheckAddr, since resolving it here would race the connection.
func (p *Policy) CheckNet(method, rawURL string) error {
	if !p.Modules[Net] {
		return errors.New("net: this module has not been given network access")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("net: bad URL: %w", unwrapURL(err))
	}
	host, port, err := target(u)
	if err != nil {
		return fmt.Errorf("net: %w", err)
	}
	scope := p.Net
	if !scope.Any && !slices.ContainsFunc(scope.Allow, func(a string) bool { return admits(a, host, port, u.Scheme) }) {
		return fmt.Errorf("net: %s:%s is not a host this module may reach%s", host, port, listed("hosts", scope.Allow))
	}
	methods := scope.Methods
	if len(methods) == 0 {
		methods = defaultMethods
	}
	if !slices.Contains(methods, strings.ToLower(method)) {
		return fmt.Errorf("net: method %s is not allowed for this module (allowed: %s)", strings.ToUpper(method), strings.ToUpper(strings.Join(methods, " ")))
	}
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil && !scope.Private && privateAddr(ip) {
		return fmt.Errorf("net: %s is a private or local address, which this module may not reach", ip)
	}
	return nil
}

// CheckAddr reports why a script holding p may not connect to ip, the address
// host resolved to, or nil if it may: a loopback, private, link-local,
// multicast, unspecified or reserved address needs (private #t). It is the
// check the installed net module makes when a connection is made; host is
// only for the message.
func (p *Policy) CheckAddr(host string, ip netip.Addr) error {
	if !p.Modules[Net] {
		return errors.New("net: this module has not been given network access")
	}
	if !ip.IsValid() {
		return fmt.Errorf("net: %s: no address to connect to", host)
	}
	if p.Net.Private || !privateAddr(ip) {
		return nil
	}
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); host == "" || err == nil {
		return fmt.Errorf("net: %s is a private or local address, which this module may not reach", ip)
	}
	return fmt.Errorf("net: %s resolves to %s, a private or local address, which this module may not reach", host, ip)
}

// CheckNetEvent judges ev for a script holding p: CheckNet for a request or a
// redirect hop, CheckAddr for a connection. It is all a WithNetCheck function
// needs for a host whose callers each have a Policy.
func (p *Policy) CheckNetEvent(ev NetEvent) error {
	switch ev.Stage {
	case NetRequest, NetRedirect:
		return p.CheckNet(ev.Method, ev.URL)
	case NetConnect:
		return p.CheckAddr(ev.Host, ev.Addr.Addr())
	}
	return fmt.Errorf("net: unknown stage %q", ev.Stage)
}

// CheckExec reports why a script holding p may not start the command name (the
// first element of proc-run's list), or nil if it may. A name is allowed when
// it is on p's list, or is the file a listed name resolves to on this machine.
func (p *Policy) CheckExec(name string) error {
	if !p.Modules[Exec] {
		return errors.New("exec: this module has not been given permission to run programs")
	}
	if p.Exec.Any {
		return nil
	}
	cmds := newCommands(p.Exec)
	if _, ok := cmds.paths[name]; ok {
		return nil
	}
	if err, ok := cmds.err[name]; ok {
		return fmt.Errorf("exec: %s is allowed for this module but cannot be run here: %w", name, err)
	}
	return fmt.Errorf("exec: %s is not a command this module may run%s", name, listed("commands", p.Exec.Allow))
}

// listed renders an allow list for an error: " (allowed hosts: a, b)".
func listed(what string, allow []string) string {
	if len(allow) == 0 {
		return fmt.Sprintf(" (it may reach no %s)", what)
	}
	return fmt.Sprintf(" (allowed %s: %s)", what, strings.Join(allow, ", "))
}

// ModuleOf names the module that installed the procedure called name ("net"
// for http-request), or "" for one that belongs to none (require, modules,
// module-info) or to nothing std installed. A Gate uses it to ask a caller's
// Policy whether the module is granted to it at all.
func (r *Runtime) ModuleOf(name string) string { return r.owner[name] }
