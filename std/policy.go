package std

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fclaude/brote/scheme"
)

// The modules a policy can grant, in the order they are installed and printed.
const (
	Path     = "path"
	Encoding = "encoding"
	Regexp   = "regexp"
	Digest   = "digest"
	Time     = "time"
	Rand     = "rand"
	Env      = "env"
	FS       = "fs"
	Net      = "net"
	Exec     = "exec"
)

// Modules lists every module name.
var Modules = []string{Path, Encoding, Regexp, Digest, Time, Rand, Env, FS, Net, Exec}

// scoped modules cannot be granted without saying what they reach.
var scoped = map[string]bool{Env: true, FS: true, Net: true, Exec: true}

// Profiles are named sets of modules for a policy's (profile NAME) clause.
// "pure" is deterministic and touches nothing; "safe" adds the clock and
// randomness; "full" is every module, unscoped. The modules that reach the
// machine (env, fs, net, exec) are only in "full".
var Profiles = []string{"pure", "safe", "full"}

// Policy is what a script may reach. The zero Policy grants nothing. Build one
// with ParsePolicy, Allow, or by filling the fields; Install normalises it.
type Policy struct {
	// Modules are the granted modules. A scoped module (env, fs, net, exec)
	// also needs the matching scope below to name something.
	Modules map[string]bool
	Env     EnvScope
	FS      FSScope
	Net     NetScope
	Exec    ExecScope
	Time    TimeScope
	Digest  DigestScope
}

// EnvScope names the environment variables env-get and env-list can see.
type EnvScope struct {
	Any   bool
	Allow []string
}

// FSScope names the directory trees fs-* can reach. Write access includes
// read access to the same tree.
type FSScope struct {
	AnyRead, AnyWrite bool
	Read, Write       []string
	MaxFile           int64 // bytes read or written by one call
}

// NetScope names the hosts http-request can reach. An Allow pattern is
// "host", "host:port" or "*.suffix"; without a port it means the scheme's
// default one (80 for http, 443 for https). Private lets a request reach
// loopback, private, link-local and metadata addresses, which are refused by
// default whatever name resolves to them.
type NetScope struct {
	Any     bool
	Allow   []string
	Methods []string // lower case; default get and head
	Private bool
	MaxBody int64
	Timeout time.Duration
}

// ExecScope names the commands proc-run can start: a bare name (found on PATH
// when the policy is installed) or an absolute path. Env lists the variables
// the child inherits; it gets nothing else.
type ExecScope struct {
	Any       bool
	Allow     []string
	Env       EnvScope
	Timeout   time.Duration
	MaxOutput int64
}

// TimeScope bounds time-sleep.
type TimeScope struct {
	MaxSleep time.Duration
}

// DigestScope names the public keys the digest module's verify procedures
// accept, as lower-case hex (an Ed25519 key is 32 bytes, an ECDSA P-256
// compressed point 33). Signing needs no grant: the private key is the
// caller's own credential. With no keys the module hashes and signs but
// verifies nothing — a key the policy does not name is an error, not a #f.
type DigestScope struct {
	Any  bool
	Keys []string
}

// grantedText renders the key list for an error message.
func (d DigestScope) grantedText() string {
	if d.Any {
		return "*"
	}
	if len(d.Keys) == 0 {
		return "none"
	}
	return strings.Join(d.Keys, ", ")
}

// Default caps, applied to any limit a policy leaves unset.
const (
	defMaxFile   = 16 * mib
	defMaxBody   = 5 * mib
	defNetTime   = 30 * time.Second
	defExecTime  = 30 * time.Second
	defMaxOutput = 1 * mib
	defMaxSleep  = 10 * time.Second
)

var defaultMethods = []string{"get", "head"}

var allMethods = []string{"delete", "get", "head", "options", "patch", "post", "put"}

// Clone returns a deep copy.
func (p *Policy) Clone() *Policy {
	c := *p
	c.Modules = maps.Clone(p.Modules)
	c.Env.Allow = slices.Clone(p.Env.Allow)
	c.FS.Read = slices.Clone(p.FS.Read)
	c.FS.Write = slices.Clone(p.FS.Write)
	c.Net.Allow = slices.Clone(p.Net.Allow)
	c.Net.Methods = slices.Clone(p.Net.Methods)
	c.Exec.Allow = slices.Clone(p.Exec.Allow)
	c.Exec.Env.Allow = slices.Clone(p.Exec.Env.Allow)
	c.Digest.Keys = slices.Clone(p.Digest.Keys)
	return &c
}

// Granted lists the granted modules in canonical order.
func (p *Policy) Granted() []string {
	var out []string
	for _, m := range Modules {
		if p.Modules[m] {
			out = append(out, m)
		}
	}
	return out
}

// Has reports whether module m is granted.
func (p *Policy) Has(m string) bool { return p.Modules[m] }

// Resolve returns a copy whose relative directory trees are made absolute
// against base (the current directory when base is empty).
func (p *Policy) Resolve(base string) *Policy {
	c := p.Clone()
	abs := func(paths []string) {
		for i, s := range paths {
			if !filepath.IsAbs(s) {
				s = filepath.Join(base, s)
			}
			if a, err := filepath.Abs(s); err == nil {
				s = a
			}
			paths[i] = filepath.Clean(s)
		}
	}
	abs(c.FS.Read)
	abs(c.FS.Write)
	c.normalize()
	return c
}

// normalize sorts and dedupes every set, fills unset limits with the defaults
// and drops scope of modules that are not granted, so equal grants compare
// equal and print the same.
func (p *Policy) normalize() {
	if p.Modules == nil {
		p.Modules = map[string]bool{}
	}
	for m, on := range p.Modules {
		if !on {
			delete(p.Modules, m)
		}
	}
	set := func(s []string) []string {
		if len(s) == 0 {
			return nil
		}
		s = slices.Clone(s)
		slices.Sort(s)
		return slices.Compact(s)
	}
	if !p.Modules[Env] {
		p.Env = EnvScope{}
	}
	if !p.Modules[FS] {
		p.FS = FSScope{}
	}
	if !p.Modules[Net] {
		p.Net = NetScope{}
	}
	if !p.Modules[Exec] {
		p.Exec = ExecScope{}
	}
	if !p.Modules[Time] {
		p.Time = TimeScope{}
	}
	if !p.Modules[Digest] {
		p.Digest = DigestScope{}
	}
	p.Env.Allow = set(p.Env.Allow)
	p.FS.Read, p.FS.Write = set(p.FS.Read), set(p.FS.Write)
	// Write includes read, so a read entry a write entry covers says nothing,
	// and one grant has one spelling.
	switch {
	case p.FS.AnyWrite:
		p.FS.AnyRead = true
		p.FS.Read = nil
	case p.FS.AnyRead:
		p.FS.Read = nil
	default:
		p.FS.Read = slices.DeleteFunc(p.FS.Read, func(r string) bool {
			return slices.ContainsFunc(p.FS.Write, func(w string) bool { return pathCovers(w, r) })
		})
		p.FS.Read = set(p.FS.Read)
	}
	p.Net.Allow, p.Net.Methods = set(p.Net.Allow), set(p.Net.Methods)
	p.Exec.Allow, p.Exec.Env.Allow = set(p.Exec.Allow), set(p.Exec.Env.Allow)
	p.Digest.Keys = set(p.Digest.Keys)
	if p.Modules[FS] && p.FS.MaxFile == 0 {
		p.FS.MaxFile = defMaxFile
	}
	if p.Modules[Net] {
		if len(p.Net.Methods) == 0 {
			p.Net.Methods = slices.Clone(defaultMethods)
		}
		if p.Net.MaxBody == 0 {
			p.Net.MaxBody = defMaxBody
		}
		if p.Net.Timeout == 0 {
			p.Net.Timeout = defNetTime
		}
	}
	if p.Modules[Exec] {
		if p.Exec.Timeout == 0 {
			p.Exec.Timeout = defExecTime
		}
		if p.Exec.MaxOutput == 0 {
			p.Exec.MaxOutput = defMaxOutput
		}
	}
	if p.Modules[Time] && p.Time.MaxSleep == 0 {
		p.Time.MaxSleep = defMaxSleep
	}
}

// profile adds the modules of a named profile. "full" also opens every scope.
func (p *Policy) profile(name string) bool {
	if p.Modules == nil {
		p.Modules = map[string]bool{}
	}
	switch name {
	case "pure":
		for _, m := range []string{Path, Encoding, Regexp, Digest} {
			p.Modules[m] = true
		}
	case "safe":
		for _, m := range []string{Path, Encoding, Regexp, Digest, Time, Rand} {
			p.Modules[m] = true
		}
	case "full":
		for _, m := range Modules {
			p.Modules[m] = true
		}
		p.Env = EnvScope{Any: true}
		p.FS = FSScope{AnyRead: true, AnyWrite: true, MaxFile: gib}
		p.Net = NetScope{Any: true, Methods: slices.Clone(allMethods), Private: true, MaxBody: 256 * mib, Timeout: 5 * time.Minute}
		p.Exec = ExecScope{Any: true, Env: EnvScope{Any: true}, Timeout: 10 * time.Minute, MaxOutput: 64 * mib}
		p.Time = TimeScope{MaxSleep: time.Hour}
		p.Digest = DigestScope{Any: true}
	default:
		return false
	}
	return true
}

// Full is the policy that grants everything unscoped, which is what the brote
// command runs its own scripts under.
func Full() *Policy {
	p := &Policy{}
	p.profile("full")
	p.normalize()
	return p
}

// Grant is one piece of a policy built in Go.
type Grant func(*Policy)

// Allow builds a policy from grants. It grants nothing a Grant does not name.
func Allow(grants ...Grant) *Policy {
	p := &Policy{Modules: map[string]bool{}}
	for _, g := range grants {
		g(p)
	}
	p.normalize()
	return p
}

// Profile grants a named profile ("pure", "safe" or "full").
func Profile(name string) Grant {
	return func(p *Policy) {
		if !p.profile(name) {
			panic("std: unknown profile " + name)
		}
	}
}

// Module grants unscoped modules by name (path, encoding, regexp, digest,
// time, rand).
func Module(names ...string) Grant {
	return func(p *Policy) {
		for _, n := range names {
			p.Modules[n] = true
		}
	}
}

// EnvVars grants env-get and env-list over the named variables.
func EnvVars(names ...string) Grant {
	return func(p *Policy) {
		p.Modules[Env] = true
		p.Env.Allow = append(p.Env.Allow, names...)
	}
}

// FSRead grants read access to directory trees.
func FSRead(paths ...string) Grant {
	return func(p *Policy) {
		p.Modules[FS] = true
		p.FS.Read = append(p.FS.Read, paths...)
	}
}

// FSWrite grants write (and read) access to directory trees.
func FSWrite(paths ...string) Grant {
	return func(p *Policy) {
		p.Modules[FS] = true
		p.FS.Write = append(p.FS.Write, paths...)
	}
}

// NetHosts grants http-request to hosts (see NetScope for the patterns).
func NetHosts(hosts ...string) Grant {
	return func(p *Policy) {
		p.Modules[Net] = true
		p.Net.Allow = append(p.Net.Allow, hosts...)
	}
}

// ExecCommands grants proc-run over commands.
func ExecCommands(cmds ...string) Grant {
	return func(p *Policy) {
		p.Modules[Exec] = true
		p.Exec.Allow = append(p.Exec.Allow, cmds...)
	}
}

// Datum is the policy as Scheme data, the form ParsePolicy reads:
// (policy (path) (fs (read "/a") (max-file 16777216)) ...). Parsing the
// printed form gives back an equal policy.
func (p *Policy) Datum() scheme.Value {
	items := []scheme.Value{scheme.Sym("policy")}
	for _, m := range p.Granted() {
		items = append(items, p.clause(m))
	}
	return scheme.List(items...)
}

// String prints the policy in the syntax ParsePolicy reads.
func (p *Policy) String() string { return scheme.Write(p.Datum()) }

// clause is module m's section of the policy as data.
func (p *Policy) clause(m string) scheme.Value {
	strs := func(any bool, s []string) []scheme.Value {
		if any {
			return []scheme.Value{scheme.Sym("*")}
		}
		out := make([]scheme.Value, len(s))
		for i, x := range s {
			out[i] = scheme.Str(x)
		}
		return out
	}
	field := func(name string, vs ...scheme.Value) scheme.Value {
		return scheme.Cons(scheme.Sym(name), scheme.List(vs...))
	}
	dur := func(d time.Duration) scheme.Value { return scheme.Str(d.String()) }
	items := []scheme.Value{scheme.Sym(m)}
	switch m {
	case Env:
		items = append(items, field("allow", strs(p.Env.Any, p.Env.Allow)...))
	case FS:
		if p.FS.AnyRead || len(p.FS.Read) > 0 {
			items = append(items, field("read", strs(p.FS.AnyRead, p.FS.Read)...))
		}
		if p.FS.AnyWrite || len(p.FS.Write) > 0 {
			items = append(items, field("write", strs(p.FS.AnyWrite, p.FS.Write)...))
		}
		items = append(items, field("max-file", scheme.Int(p.FS.MaxFile)))
	case Net:
		items = append(items,
			field("allow", strs(p.Net.Any, p.Net.Allow)...),
			field("methods", strs(false, p.Net.Methods)...),
			field("private", scheme.Bool(p.Net.Private)),
			field("max-body", scheme.Int(p.Net.MaxBody)),
			field("timeout", dur(p.Net.Timeout)))
	case Exec:
		items = append(items,
			field("allow", strs(p.Exec.Any, p.Exec.Allow)...),
			field("timeout", dur(p.Exec.Timeout)),
			field("max-output", scheme.Int(p.Exec.MaxOutput)),
			scheme.Cons(scheme.Sym("env"), scheme.List(field("allow", strs(p.Exec.Env.Any, p.Exec.Env.Allow)...))))
	case Time:
		items = append(items, field("max-sleep", dur(p.Time.MaxSleep)))
	case Digest:
		if p.Digest.Any || len(p.Digest.Keys) > 0 {
			items = append(items, field("keys", strs(p.Digest.Any, p.Digest.Keys)...))
		}
	}
	return scheme.List(items...)
}
