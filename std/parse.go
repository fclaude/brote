package std

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/fclaude/brote/scheme"
)

// ParsePolicy reads a (policy clause...) form. The text is read as data and
// never evaluated. Anything it does not understand is an error: an unknown
// clause, a module named twice, a scoped module that names nothing.
//
//	(policy
//	  (profile safe)                    ; pure, safe or full; clauses refine it
//	  (regexp)                          ; an unscoped module by name
//	  (env  (allow "HOME" "LANG"))
//	  (fs   (read "data") (write "out") (max-file 16MB))
//	  (net  (allow "api.example.com" "*.github.com:443") (methods get post)
//	        (private #f) (max-body 5MB) (timeout 10s))
//	  (exec (allow "git" "/usr/bin/jq") (timeout 30s) (max-output 1MB)
//	        (env (allow "PATH")))
//	  (time (max-sleep 5s)))
//
// In allow lists and paths, * (a symbol) means everything.
func ParsePolicy(src string) (*Policy, error) {
	forms, err := scheme.Read(src)
	if err != nil {
		return nil, fmt.Errorf("std: policy: %w", err)
	}
	if len(forms) != 1 {
		return nil, errors.New("std: policy: expected one (policy ...) form")
	}
	items, ok := forms[0].Slice()
	if !ok || len(items) == 0 || !isSym(items[0], "policy") {
		return nil, errors.New("std: policy: expected a (policy ...) form")
	}
	p, err := parseClauses(items[1:])
	if err != nil {
		return nil, fmt.Errorf("std: policy: %w", err)
	}
	return p, nil
}

func isSym(v scheme.Value, name string) bool { return v.Kind == scheme.KSymbol && v.S == name }

// parseClauses builds a policy from the clauses of a (policy ...) or
// (requires ...) form.
func parseClauses(clauses []scheme.Value) (*Policy, error) {
	type clause struct {
		name string
		args []scheme.Value
	}
	var cls []clause
	var profiles []string
	for _, c := range clauses {
		items, ok := c.Slice()
		if !ok || len(items) == 0 || items[0].Kind != scheme.KSymbol {
			return nil, fmt.Errorf("a clause is a list that starts with a name, got %s", scheme.Write(c))
		}
		if items[0].S == "profile" {
			if len(items) != 2 {
				return nil, errors.New("(profile NAME) takes one name")
			}
			name, _ := atom(items[1])
			if !slices.Contains(Profiles, name) {
				return nil, fmt.Errorf("unknown profile %s (known: %s)", scheme.Write(items[1]), strings.Join(Profiles, ", "))
			}
			profiles = append(profiles, name)
			continue
		}
		cls = append(cls, clause{items[0].S, items[1:]})
	}
	p := &Policy{Modules: map[string]bool{}}
	for _, name := range profiles {
		p.profile(name)
	}
	seen := map[string]bool{}
	for _, c := range cls {
		if !slices.Contains(Modules, c.name) {
			return nil, fmt.Errorf("unknown clause %q (modules: %s)", c.name, strings.Join(Modules, ", "))
		}
		if seen[c.name] {
			return nil, fmt.Errorf("(%s ...) appears twice", c.name)
		}
		seen[c.name] = true
		if err := p.parseModule(c.name, c.args); err != nil {
			return nil, fmt.Errorf("(%s ...): %w", c.name, err)
		}
	}
	p.normalize()
	return p, nil
}

// fields splits the arguments of a module clause into (name arg...) fields.
func fields(args []scheme.Value) (map[string][]scheme.Value, error) {
	out := map[string][]scheme.Value{}
	for _, a := range args {
		items, ok := a.Slice()
		if !ok || len(items) == 0 || items[0].Kind != scheme.KSymbol {
			return nil, fmt.Errorf("expected (name value...), got %s", scheme.Write(a))
		}
		if _, dup := out[items[0].S]; dup {
			return nil, fmt.Errorf("(%s ...) appears twice", items[0].S)
		}
		out[items[0].S] = items[1:]
	}
	return out, nil
}

// only fails if f has a field outside names.
func only(f map[string][]scheme.Value, names ...string) error {
	for k := range f {
		if !slices.Contains(names, k) {
			return fmt.Errorf("unknown field %q (known: %s)", k, strings.Join(names, ", "))
		}
	}
	return nil
}

// words reads atoms; a lone * makes any true.
func words(vs []scheme.Value) (list []string, any bool, err error) {
	for _, v := range vs {
		s, ok := atom(v)
		if !ok {
			return nil, false, fmt.Errorf("expected a name or string, got %s", scheme.Write(v))
		}
		if v.Kind == scheme.KSymbol && s == "*" {
			any = true
			continue
		}
		list = append(list, s)
	}
	if any && len(list) > 0 {
		return nil, false, errors.New("* cannot be mixed with other entries")
	}
	return list, any, nil
}

func one(vs []scheme.Value, what string) (scheme.Value, error) {
	if len(vs) != 1 {
		return scheme.Value{}, fmt.Errorf("(%s ...) takes one value", what)
	}
	return vs[0], nil
}

// parseModule reads module m's clause into p.
func (p *Policy) parseModule(m string, args []scheme.Value) error {
	f, err := fields(args)
	if err != nil {
		return err
	}
	// A section replaces what a profile gave the module.
	p.Modules[m] = true
	switch m {
	case Path, Encoding, Regexp, Rand:
		if len(args) > 0 {
			return errors.New("takes no fields")
		}
	case Digest:
		if err := only(f, "keys"); err != nil {
			return err
		}
		p.Digest = DigestScope{}
		var err error
		if p.Digest.Keys, p.Digest.Any, err = allowList(f, "keys"); err != nil {
			return err
		}
		for i, k := range p.Digest.Keys {
			k = strings.ToLower(k)
			if b, err := hex.DecodeString(k); err != nil || len(b) == 0 {
				return fmt.Errorf("key %q is not hex", k)
			}
			p.Digest.Keys[i] = k
		}
	case Time:
		if err := only(f, "max-sleep"); err != nil {
			return err
		}
		p.Time = TimeScope{}
		if v, ok := f["max-sleep"]; ok {
			x, err := one(v, "max-sleep")
			if err != nil {
				return err
			}
			if p.Time.MaxSleep, err = parseDuration(x); err != nil {
				return err
			}
		}
	case Env:
		if err := only(f, "allow"); err != nil {
			return err
		}
		p.Env = EnvScope{}
		var err error
		if p.Env.Allow, p.Env.Any, err = allowList(f, "allow"); err != nil {
			return err
		}
		if len(p.Env.Allow) == 0 && !p.Env.Any {
			return errors.New("needs (allow NAME...) or (allow *)")
		}
		for _, n := range p.Env.Allow {
			if n == "" || strings.ContainsAny(n, "=\x00") {
				return fmt.Errorf("%q is not a variable name", n)
			}
		}
	case FS:
		return p.parseFS(f)
	case Net:
		return p.parseNet(f)
	case Exec:
		return p.parseExec(f)
	}
	return nil
}

func allowList(f map[string][]scheme.Value, name string) ([]string, bool, error) {
	vs, ok := f[name]
	if !ok {
		return nil, false, nil
	}
	l, any, err := words(vs)
	if err != nil {
		return nil, false, fmt.Errorf("(%s ...): %w", name, err)
	}
	return l, any, nil
}

func (p *Policy) parseFS(f map[string][]scheme.Value) error {
	if err := only(f, "read", "write", "max-file"); err != nil {
		return err
	}
	p.FS = FSScope{}
	var err error
	if p.FS.Read, p.FS.AnyRead, err = allowList(f, "read"); err != nil {
		return err
	}
	if p.FS.Write, p.FS.AnyWrite, err = allowList(f, "write"); err != nil {
		return err
	}
	if len(p.FS.Read)+len(p.FS.Write) == 0 && !p.FS.AnyRead && !p.FS.AnyWrite {
		return errors.New("needs (read PATH...) or (write PATH...)")
	}
	for _, s := range append(slices.Clone(p.FS.Read), p.FS.Write...) {
		if s == "" || strings.ContainsRune(s, 0) {
			return fmt.Errorf("%q is not a path", s)
		}
	}
	if v, ok := f["max-file"]; ok {
		x, err := one(v, "max-file")
		if err != nil {
			return err
		}
		if p.FS.MaxFile, err = parseSize(x); err != nil {
			return err
		}
	}
	return nil
}

func (p *Policy) parseNet(f map[string][]scheme.Value) error {
	if err := only(f, "allow", "methods", "private", "max-body", "timeout"); err != nil {
		return err
	}
	p.Net = NetScope{}
	var err error
	if p.Net.Allow, p.Net.Any, err = allowList(f, "allow"); err != nil {
		return err
	}
	if len(p.Net.Allow) == 0 && !p.Net.Any {
		return errors.New("needs (allow HOST...) or (allow *)")
	}
	for i, h := range p.Net.Allow {
		c, err := canonHostPattern(h)
		if err != nil {
			return err
		}
		p.Net.Allow[i] = c
	}
	if v, ok := f["methods"]; ok {
		l, any, err := words(v)
		if err != nil {
			return fmt.Errorf("(methods ...): %w", err)
		}
		if any {
			l = allMethods
		}
		for _, mth := range l {
			mth = strings.ToLower(mth)
			if !slices.Contains(allMethods, mth) {
				return fmt.Errorf("unknown method %q", mth)
			}
			p.Net.Methods = append(p.Net.Methods, mth)
		}
		if len(p.Net.Methods) == 0 {
			return errors.New("(methods ...) names no method")
		}
	}
	if v, ok := f["private"]; ok {
		x, err := one(v, "private")
		if err != nil {
			return err
		}
		if x.Kind != scheme.KBool {
			return errors.New("(private ...) takes #t or #f")
		}
		p.Net.Private = x.IsTrue()
	}
	if v, ok := f["max-body"]; ok {
		x, err := one(v, "max-body")
		if err != nil {
			return err
		}
		if p.Net.MaxBody, err = parseSize(x); err != nil {
			return err
		}
	}
	if v, ok := f["timeout"]; ok {
		x, err := one(v, "timeout")
		if err != nil {
			return err
		}
		if p.Net.Timeout, err = parseDuration(x); err != nil {
			return err
		}
	}
	return nil
}

func (p *Policy) parseExec(f map[string][]scheme.Value) error {
	if err := only(f, "allow", "timeout", "max-output", "env"); err != nil {
		return err
	}
	p.Exec = ExecScope{}
	var err error
	if p.Exec.Allow, p.Exec.Any, err = allowList(f, "allow"); err != nil {
		return err
	}
	if len(p.Exec.Allow) == 0 && !p.Exec.Any {
		return errors.New("needs (allow COMMAND...) or (allow *)")
	}
	for _, c := range p.Exec.Allow {
		if c == "" || strings.ContainsRune(c, 0) {
			return fmt.Errorf("%q is not a command", c)
		}
		if strings.ContainsAny(c, `/\`) && !filepath.IsAbs(c) {
			return fmt.Errorf("command %q has a directory: give an absolute path or a bare name", c)
		}
	}
	if v, ok := f["timeout"]; ok {
		x, err := one(v, "timeout")
		if err != nil {
			return err
		}
		if p.Exec.Timeout, err = parseDuration(x); err != nil {
			return err
		}
	}
	if v, ok := f["max-output"]; ok {
		x, err := one(v, "max-output")
		if err != nil {
			return err
		}
		if p.Exec.MaxOutput, err = parseSize(x); err != nil {
			return err
		}
	}
	if v, ok := f["env"]; ok {
		inner, err := fields(v)
		if err != nil {
			return fmt.Errorf("(env ...): %w", err)
		}
		if err := only(inner, "allow"); err != nil {
			return fmt.Errorf("(env ...): %w", err)
		}
		if p.Exec.Env.Allow, p.Exec.Env.Any, err = allowList(inner, "allow"); err != nil {
			return err
		}
	}
	return nil
}

// splitHostPattern breaks "host", "host:port", "[v6]" or "[v6]:port" apart.
func splitHostPattern(s string) (host, port string, err error) {
	if strings.HasPrefix(s, "[") {
		if h, p, e := net.SplitHostPort(s); e == nil {
			host, port = "["+h+"]", p
		} else if strings.HasSuffix(s, "]") {
			host = s
		} else {
			return "", "", fmt.Errorf("bad host pattern %q", s)
		}
	} else {
		switch strings.Count(s, ":") {
		case 0:
			host = s
		case 1:
			host, port, _ = strings.Cut(s, ":")
		default:
			return "", "", fmt.Errorf("host pattern %q: write an IPv6 address in brackets", s)
		}
	}
	if port != "" {
		if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
			return "", "", fmt.Errorf("host pattern %q: bad port", s)
		}
	}
	return host, port, nil
}

// canonHostPattern validates a net allow entry and lower-cases it.
func canonHostPattern(s string) (string, error) {
	s = strings.ToLower(s)
	if strings.ContainsAny(s, "/@?# \t\n\x00") {
		return "", fmt.Errorf("%q is not a host pattern: use host, host:port or *.suffix, without a scheme or path", s)
	}
	host, port, err := splitHostPattern(s)
	if err != nil {
		return "", err
	}
	bad := host == "" || strings.IndexFunc(host, unicode.IsSpace) >= 0
	if strings.Contains(host, "*") && !(strings.HasPrefix(host, "*.") && len(host) > 2 && !strings.Contains(host[2:], "*")) {
		bad = true
	}
	if bad {
		return "", fmt.Errorf("%q is not a host pattern: use host, host:port or *.suffix, without a scheme or path", s)
	}
	if port != "" {
		return host + ":" + port, nil
	}
	return host, nil
}
