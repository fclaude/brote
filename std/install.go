package std

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/fclaude/brote/scheme"
)

//go:embed all:prelude
var preludeFS embed.FS

// Event is one use of the machine by a script, reported to WithAudit: a file
// written or removed, a request sent, a process started.
type Event struct {
	Module string // fs, net, exec, ...
	Op     string // for example write, request, run
	Target string // the path, URL or command
	Err    error  // nil when the operation succeeded
}

// Option configures Install.
type Option func(*installer)

// WithBaseDir sets the directory relative paths in the policy and in the
// script's file arguments are taken against. The default is the current
// directory. Pass the same directory to scheme.WithBaseDir.
func WithBaseDir(dir string) Option { return func(s *installer) { s.base = dir } }

// WithAudit reports each file write or removal, request and process to fn,
// from the goroutine that is evaluating. When scripts have tasks that is
// several goroutines, but fn is called one event at a time, so it needs no
// locking of its own. It sees what happened whether or not a
// scheme.Gate is installed.
func WithAudit(fn func(Event)) Option { return func(s *installer) { s.audit = fn } }

// WithNetCheck has fn judge each network step the net module is about to take,
// after the installed policy has allowed it: the request a script asked for,
// every redirect hop, and every connection, at the moment it is made. fn gets
// the request's context, which carries the values the host put in the
// evaluation's, so a host that runs several callers on one interpreter can
// narrow each step to the calling one's grant (Policy.CheckNetEvent). An error
// refuses the step, and is the text the script and WithAudit see; return a
// *scheme.Denied to make the refusal one guard cannot catch, as a Gate's is.
// fn can only narrow: what the installed policy refuses is refused before fn
// is asked.
//
// fn is called from the evaluating goroutine for a request and its redirects
// and from the dialing goroutine for a connection, and from several at once
// when scripts have tasks, so it must be safe for concurrent use. It should
// honour ctx. While it is set, a request's connections serve that request
// alone: a connection fn allowed for one caller never carries another's.
func WithNetCheck(fn func(ctx context.Context, ev NetEvent) error) Option {
	return func(s *installer) { s.netCheck = fn }
}

// installer is what a module's install function works with.
type installer struct {
	pol      *Policy // resolved and normalised; the module's scope is read from it
	base     string
	audit    func(Event)
	netCheck func(context.Context, NetEvent) error
	prims    []*scheme.Prim
	closers  []io.Closer
	fs       *fsys // set by the fs module
	module   string
	owner    map[string]string // procedure name -> the module that installed it
}

func (s *installer) add(p *scheme.Prim) {
	s.prims = append(s.prims, p)
	if s.module != "" {
		s.owner[p.Name] = s.module
	}
}

func (s *installer) report(module, op, target string, err error) {
	if s.audit != nil {
		s.audit(Event{Module: module, Op: op, Target: target, Err: err})
	}
}

// moduleInstallers is filled by each module's file.
var moduleInstallers = map[string]func(*installer) error{}

// Runtime is a policy installed into an interpreter.
type Runtime struct {
	pol     *Policy
	closers []io.Closer
	fs      *fsys
	owner   map[string]string
}

// Policy returns the grant in force, normalised, with directories made absolute.
func (r *Runtime) Policy() *Policy { return r.pol.Clone() }

// Granted lists the installed modules.
func (r *Runtime) Granted() []string { return r.pol.Granted() }

// Close releases what the installed modules hold open (directory handles). Call
// it when the interpreter is done; the procedures fail afterwards.
func (r *Runtime) Close() error {
	var errs []error
	for _, c := range r.closers {
		errs = append(errs, c.Close())
	}
	r.closers = nil
	return errors.Join(errs...)
}

// Install registers the procedures of every module p grants, scoped as p says,
// plus require, modules and module-info. A module p does not grant registers
// nothing. Call it before Freeze, once per interpreter. If it returns an error
// the interpreter may have gained some procedures: discard it.
func Install(in *scheme.Interp, p *Policy, opts ...Option) (*Runtime, error) {
	s := &installer{owner: map[string]string{}}
	for _, o := range opts {
		o(s)
	}
	s.pol = p.Resolve(s.base)
	if err := s.pol.validate(); err != nil {
		return nil, err
	}
	if s.audit != nil {
		fn, mu := s.audit, new(sync.Mutex)
		s.audit = func(e Event) {
			mu.Lock()
			defer mu.Unlock()
			fn(e)
		}
	}
	for _, m := range s.pol.Granted() {
		fn := moduleInstallers[m]
		s.module = m
		if fn == nil {
			s.close()
			return nil, fmt.Errorf("std: module %q is not available in this build", m)
		}
		if err := fn(s); err != nil {
			s.close()
			return nil, fmt.Errorf("std: %s: %w", m, err)
		}
	}
	s.module = ""
	s.addIntrospection()
	for _, pr := range s.prims {
		if err := in.Register(pr); err != nil {
			s.close()
			return nil, err
		}
	}
	for _, m := range s.pol.Granted() {
		src, err := preludeFS.ReadFile("prelude/" + m + ".scm")
		if err != nil {
			continue
		}
		if _, err := in.EvalLibrary(context.Background(), "std/"+m+".scm", string(src)); err != nil {
			s.close()
			return nil, fmt.Errorf("std: prelude %s: %w", m, err)
		}
	}
	return &Runtime{pol: s.pol, closers: s.closers, fs: s.fs, owner: s.owner}, nil
}

func (s *installer) close() {
	for _, c := range s.closers {
		_ = c.Close()
	}
	s.closers = nil
}

// validate rejects a policy that grants a scoped module without a scope, or an
// unknown module, which a policy built in Go (not parsed) can contain.
func (p *Policy) validate() error {
	for m := range p.Modules {
		if !slices.Contains(Modules, m) {
			return fmt.Errorf("std: unknown module %q", m)
		}
	}
	empty := func(m string, ok bool) error {
		if !ok {
			return fmt.Errorf("std: module %s is granted but names nothing it may reach", m)
		}
		return nil
	}
	if p.Modules[Env] {
		if err := empty(Env, p.Env.Any || len(p.Env.Allow) > 0); err != nil {
			return err
		}
	}
	if p.Modules[FS] {
		if err := empty(FS, p.FS.AnyRead || p.FS.AnyWrite || len(p.FS.Read)+len(p.FS.Write) > 0); err != nil {
			return err
		}
	}
	if p.Modules[Net] {
		if err := empty(Net, p.Net.Any || len(p.Net.Allow) > 0); err != nil {
			return err
		}
		for _, h := range p.Net.Allow {
			if _, err := canonHostPattern(h); err != nil {
				return fmt.Errorf("std: net: %w", err)
			}
		}
	}
	if p.Modules[Exec] {
		if err := empty(Exec, p.Exec.Any || len(p.Exec.Allow) > 0); err != nil {
			return err
		}
	}
	return nil
}

// addIntrospection registers require, modules and module-info, which let a
// script (a plugin, especially) see what it was given and fail clearly when it
// was not given what it needs.
func (s *installer) addIntrospection() {
	granted := s.pol.Granted()
	has := func(m string) bool { return slices.Contains(granted, m) }
	name := func(c *scheme.Call, i int) (string, error) {
		switch v := c.Args[i]; v.Kind {
		case scheme.KSymbol, scheme.KString:
			return v.S, nil
		}
		return "", fmt.Errorf("argument %d: expected a module name, got %s", i+1, scheme.Write(c.Args[i]))
	}
	s.add(&scheme.Prim{
		Name: "require",
		Meta: scheme.Meta{
			Doc:      "Check that the host granted each named module, for example (require 'fs 'net). Returns #t, or raises an error that names the module and lists what is granted.",
			Params:   []scheme.Param{{Name: "module", Type: "symbol", Doc: "module name"}},
			Variadic: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			for i := range c.Args {
				m, err := name(c, i)
				if err != nil {
					return scheme.Value{}, err
				}
				if !slices.Contains(Modules, m) {
					return scheme.Value{}, fmt.Errorf("unknown module %q (modules: %s)", m, strings.Join(Modules, ", "))
				}
				if !has(m) {
					return scheme.Value{}, fmt.Errorf("module %s is not granted (granted: %s)", m, grantedText(granted))
				}
			}
			return scheme.True, nil
		},
	})
	s.add(&scheme.Prim{
		Name: "modules",
		Meta: scheme.Meta{Doc: "The modules the host granted, as a list of symbols."},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			out := make([]scheme.Value, len(granted))
			for i, m := range granted {
				out[i] = scheme.Sym(m)
			}
			return scheme.List(out...), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "module-info",
		Meta: scheme.Meta{
			Doc:    "What a granted module may reach, as data in the policy's own syntax: (module-info 'fs) gives ((read \"/data\") (max-file 16777216)). #f when the module is not granted.",
			Params: []scheme.Param{{Name: "module", Type: "symbol", Doc: "module name"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			m, err := name(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			if !has(m) {
				return scheme.False, nil
			}
			return s.pol.clause(m).P.Cdr, nil
		},
	})
}

func grantedText(g []string) string {
	if len(g) == 0 {
		return "none"
	}
	return strings.Join(g, " ")
}
