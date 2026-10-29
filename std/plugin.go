package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fclaude/brote/scheme"
)

// Manifest is the head of a plugin file: what the plugin is and what it asks
// to reach. It is read as data. Nothing in the file runs until the host has
// decided what to grant.
//
//	(plugin
//	  (name "word-count")
//	  (version "1.2")
//	  (description "Counts words in the notes directory")
//	  (requires (fs (read "notes")) (net (allow "api.example.com"))))
//
// The rest of the file is the plugin's program.
type Manifest struct {
	Name, Version, Description string
	// Requires is what the plugin asks for, with directories made absolute
	// against the plugin file's directory. A plugin with no (requires ...) asks
	// for nothing and gets the bare language.
	Requires *Policy
	Dir      string // the plugin file's directory
	Path     string
	body     []scheme.Value
}

// ReadManifest reads a plugin file's manifest without running anything.
func ReadManifest(path string) (*Manifest, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(string(src), filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.Path = path
	return m, nil
}

// ParseManifest reads the plugin source src; dir is the directory its relative
// paths mean.
func ParseManifest(src, dir string) (*Manifest, error) {
	forms, err := scheme.Read(src)
	if err != nil {
		return nil, err
	}
	if len(forms) == 0 {
		return nil, errors.New("empty plugin: it must start with a (plugin ...) form")
	}
	items, ok := forms[0].Slice()
	if !ok || len(items) == 0 || !isSym(items[0], "plugin") {
		return nil, errors.New("a plugin must start with a (plugin (name ...) ...) form")
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	m := &Manifest{Dir: dir, body: forms[1:]}
	var requires []scheme.Value
	seen := map[string]bool{}
	for _, c := range items[1:] {
		cl, ok := c.Slice()
		if !ok || len(cl) == 0 || cl[0].Kind != scheme.KSymbol {
			return nil, fmt.Errorf("plugin: expected (name ...), got %s", scheme.Write(c))
		}
		key := cl[0].S
		if seen[key] {
			return nil, fmt.Errorf("plugin: (%s ...) appears twice", key)
		}
		seen[key] = true
		text := func() (string, error) {
			if len(cl) != 2 {
				return "", fmt.Errorf("plugin: (%s ...) takes one value", key)
			}
			if cl[1].IsNumber() {
				return scheme.Display(cl[1]), nil
			}
			s, ok := atom(cl[1])
			if !ok {
				return "", fmt.Errorf("plugin: (%s ...) takes text, got %s", key, scheme.Write(cl[1]))
			}
			return s, nil
		}
		switch key {
		case "name":
			if m.Name, err = text(); err != nil {
				return nil, err
			}
		case "version":
			if m.Version, err = text(); err != nil {
				return nil, err
			}
		case "description":
			if m.Description, err = text(); err != nil {
				return nil, err
			}
		case "requires":
			requires = cl[1:]
		default:
			return nil, fmt.Errorf("plugin: unknown clause %q (known: name, version, description, requires)", key)
		}
	}
	if strings.TrimSpace(m.Name) == "" {
		return nil, errors.New("plugin: it needs a (name \"...\")")
	}
	req, err := parseClauses(requires)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: requires: %w", m.Name, err)
	}
	m.Requires = req.Resolve(dir)
	return m, nil
}

// Plan says what host would grant this plugin and what it would refuse, without
// loading anything: the host can show it to a person before they agree.
func (m *Manifest) Plan(host *Policy, hostBase string) (granted *Policy, refused []string) {
	return host.Resolve(hostBase).Intersect(m.Requires)
}

// ExcessError is returned by OpenPlugin when a plugin asks for more than the
// host allows and the host did not choose to run it degraded.
type ExcessError struct {
	Plugin  string
	Refused []string
}

func (e *ExcessError) Error() string {
	return fmt.Sprintf("plugin %s asks for more than this host allows: %s", e.Plugin, strings.Join(e.Refused, "; "))
}

// Excess is what OpenPlugin does with a plugin that asks for more than the host
// allows.
type Excess int

const (
	// ExcessFail refuses to load the plugin (the default).
	ExcessFail Excess = iota
	// ExcessDegrade loads it with only what the host allows. The plugin can see
	// what it got with (modules) and (module-info), and Plugin.Refused lists
	// what it did not.
	ExcessDegrade
)

type pluginConfig struct {
	excess   Excess
	out      io.Writer
	gate     scheme.Gate
	audit    func(Event)
	setup    func(*scheme.Interp) error
	hostBase string
}

// PluginOption configures OpenPlugin.
type PluginOption func(*pluginConfig)

// OnExcess chooses what happens when a plugin asks for more than the host
// allows.
func OnExcess(e Excess) PluginOption { return func(c *pluginConfig) { c.excess = e } }

// PluginOutput sets where the plugin's display, write and newline go.
func PluginOutput(w io.Writer) PluginOption { return func(c *pluginConfig) { c.out = w } }

// PluginGate adds the host's own gate. The plugin's interpreter always has
// Runtime.Gate in front of it, so load stays inside the grant.
func PluginGate(g scheme.Gate) PluginOption { return func(c *pluginConfig) { c.gate = g } }

// PluginAudit reports the plugin's file writes, requests and processes.
func PluginAudit(fn func(Event)) PluginOption { return func(c *pluginConfig) { c.audit = fn } }

// PluginSetup runs before the plugin's program, with the plugin's own
// interpreter, so the host can register the primitives of its own API.
func PluginSetup(fn func(*scheme.Interp) error) PluginOption {
	return func(c *pluginConfig) { c.setup = fn }
}

// HostBase is the directory relative paths in the host policy mean. The
// default is the current directory.
func HostBase(dir string) PluginOption { return func(c *pluginConfig) { c.hostBase = dir } }

// Plugin is a loaded plugin: its own interpreter, holding only what the host
// granted it, and the record of what it asked for and got.
type Plugin struct {
	Manifest *Manifest
	// Granted is the grant in force: the host's policy narrowed to the plugin's
	// request.
	Granted *Policy
	// Refused lists what the plugin asked for and did not get (empty unless
	// the plugin was loaded with ExcessDegrade).
	Refused []string
	Interp  *scheme.Interp
	rt      *Runtime
}

// OpenPlugin loads a plugin file. It reads the manifest, grants the plugin the
// intersection of host and what it requests, builds a fresh interpreter that
// holds only that, freezes registration, and runs the plugin's program in it.
// The plugin's definitions are then in Interp.Global() for the host to look up
// and Apply. Close the plugin when done.
func OpenPlugin(ctx context.Context, path string, host *Policy, opts ...PluginOption) (*Plugin, error) {
	cfg := &pluginConfig{out: io.Discard}
	for _, o := range opts {
		o(cfg)
	}
	m, err := ReadManifest(path)
	if err != nil {
		return nil, err
	}
	granted, refused := m.Plan(host, cfg.hostBase)
	if len(refused) > 0 && cfg.excess == ExcessFail {
		return nil, &ExcessError{Plugin: m.Name, Refused: refused}
	}
	in := scheme.New(scheme.WithOutput(cfg.out), scheme.WithBaseDir(m.Dir))
	rt, err := Install(in, granted, WithBaseDir(m.Dir), WithAudit(cfg.audit))
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", m.Name, err)
	}
	fail := func(err error) (*Plugin, error) {
		rt.Close()
		return nil, err
	}
	in.SetGate(rt.Gate(cfg.gate))
	if cfg.setup != nil {
		if err := cfg.setup(in); err != nil {
			return fail(fmt.Errorf("plugin %s: %w", m.Name, err))
		}
	}
	in.Freeze()
	for _, f := range m.body {
		if _, err := in.Eval(ctx, f, in.Global()); err != nil {
			return fail(fmt.Errorf("plugin %s: %s: %w", m.Name, m.Path, err))
		}
	}
	return &Plugin{Manifest: m, Granted: rt.Policy(), Refused: refused, Interp: in, rt: rt}, nil
}

// Call applies the plugin's global procedure name to args.
func (p *Plugin) Call(ctx context.Context, name string, args ...scheme.Value) (scheme.Value, error) {
	f, ok := p.Interp.Global().Lookup(name)
	if !ok || !f.IsProcedure() {
		return scheme.Value{}, fmt.Errorf("plugin %s has no procedure %q", p.Manifest.Name, name)
	}
	return p.Interp.Apply(ctx, f, args)
}

// Close releases the plugin's directory handles and connections.
func (p *Plugin) Close() error { return p.rt.Close() }
