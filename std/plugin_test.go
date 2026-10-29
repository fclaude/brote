package std

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

type pluginFixture struct{ root, dir string }

func newPluginFixture(t *testing.T) *pluginFixture {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &pluginFixture{root: root, dir: filepath.Join(root, "plug")}
	for _, d := range []string{filepath.Join(f.dir, "notes")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.file(t, "notes/a.txt", "one two three")
	f.file(t, "notes/inc.scm", "(define included 42)")
	f.file(t, "../secret.txt", "s3cret")
	f.file(t, "../outside.scm", "(define escaped #t)")
	return f
}

func (f *pluginFixture) file(t *testing.T, rel, content string) string {
	t.Helper()
	p := filepath.Join(f.dir, rel)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const wordCount = `(plugin (name "word-count") (version 1) (description "counts")
  (requires (fs (read "notes"))))
(require 'fs)
(define (count-words path) (length (string-split (fs-read-text path) " ")))
`

func TestPluginGetsOnlyWhatItAskedAndTheHostHas(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "wc.scm", wordCount)
	host := Allow(FSRead(f.root), FSWrite(f.root), NetHosts("api.x.com"), ExecCommands("echo"), Profile("safe"))
	p, err := OpenPlugin(context.Background(), path, host)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Manifest.Name != "word-count" || p.Manifest.Version != "1" || p.Manifest.Description != "counts" {
		t.Errorf("manifest %+v", p.Manifest)
	}
	if got := p.Granted.Granted(); len(got) != 1 || got[0] != FS {
		t.Errorf("granted %v, want only fs", got)
	}
	if want := []string{filepath.Join(f.dir, "notes")}; !equalStrings(p.Granted.FS.Read, want) || len(p.Granted.FS.Write) != 0 {
		t.Errorf("fs scope %+v, want read %v and no write", p.Granted.FS, want)
	}
	v, err := p.Call(context.Background(), "count-words", scheme.Str("notes/a.txt"))
	if err != nil || v.I != 3 {
		t.Errorf("count-words = %v, %v", v, err)
	}
	// It cannot read beside its notes, write at all, or use any module it did not ask for.
	for _, src := range []string{
		`(fs-read-text "../secret.txt")`, `(fs-read-text "notes/../../secret.txt")`, `(fs-read-text "` + f.root + `/secret.txt")`,
		`(fs-write-text "notes/x" "no")`, `(http-get "http://api.x.com/")`, `(proc-run '("echo" "hi"))`, `(time-now)`, `(path-join "a")`,
	} {
		if _, err := p.Interp.EvalString(context.Background(), src); err == nil {
			t.Errorf("%s worked inside the plugin", src)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "notes", "x")); err == nil {
		t.Error("the plugin wrote a file")
	}
	if err := p.Interp.Register(&scheme.Prim{Name: "late", Meta: scheme.Meta{Doc: "x"}, Handler: func(*scheme.Call) (scheme.Value, error) { return scheme.Void, nil }}); err == nil {
		t.Error("a plugin interpreter accepts new primitives after loading")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPluginExcess(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "greedy.scm", `(plugin (name "greedy")
  (requires (fs (read "notes")) (net (allow "api.x.com")) (exec (allow "rm"))))
(define asked (modules))
(define (has-net?) (module-info 'net))`)
	host := Allow(FSRead(f.root), NetHosts("other.com"))

	_, err := OpenPlugin(context.Background(), path, host)
	var ex *ExcessError
	if !errors.As(err, &ex) || ex.Plugin != "greedy" || strings.Join(ex.Refused, ";") != "exec;net api.x.com" {
		t.Fatalf("err %v, want an ExcessError for exec and net api.x.com", err)
	}

	p, err := OpenPlugin(context.Background(), path, host, OnExcess(ExcessDegrade))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if strings.Join(p.Refused, ";") != "exec;net api.x.com" {
		t.Errorf("refused %v", p.Refused)
	}
	if got := p.Granted.Granted(); len(got) != 1 || got[0] != FS {
		t.Errorf("granted %v", got)
	}
	if v, err := p.Call(context.Background(), "has-net?"); err != nil || v.IsTrue() {
		t.Errorf("(module-info 'net) in the degraded plugin = %v, %v; want #f", v, err)
	}
}

func TestPluginThatRequiresWhatItWasRefusedFailsToLoad(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "needs.scm", `(plugin (name "needs") (requires (net (allow "api.x.com"))))
(require 'net)`)
	_, err := OpenPlugin(context.Background(), path, Allow(Profile("pure")), OnExcess(ExcessDegrade))
	if err == nil || !strings.Contains(err.Error(), "net is not granted") {
		t.Errorf("got %v", err)
	}
}

// load reads any file the process can; the plugin's interpreter confines it to the grant.
func TestPluginLoadStaysInsideTheGrant(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "loader.scm", `(plugin (name "loader") (requires (fs (read "notes"))))`)
	host := Allow(FSRead(f.root))
	p, err := OpenPlugin(context.Background(), path, host)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if v, err := p.Interp.EvalString(context.Background(), `(begin (load "notes/inc.scm") included)`); err != nil || v.I != 42 {
		t.Errorf("load inside the grant: %v, %v", v, err)
	}
	for _, src := range []string{`(load "../outside.scm")`, `(load "` + f.root + `/outside.scm")`, `(load "/etc/hosts")`} {
		_, err := p.Interp.EvalString(context.Background(), src)
		var d *scheme.Denied
		if !errors.As(err, &d) {
			t.Errorf("%s: %v, want a denial", src, err)
		}
	}
	if _, ok := p.Interp.Global().Lookup("escaped"); ok {
		t.Error("a file outside the grant was loaded")
	}

	// With no fs grant at all, load is off.
	bare := f.file(t, "bare.scm", `(plugin (name "bare"))`)
	q, err := OpenPlugin(context.Background(), bare, host)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if _, err := q.Interp.EvalString(context.Background(), `(load "notes/inc.scm")`); err == nil || !strings.Contains(err.Error(), "fs module is not granted") {
		t.Errorf("load without fs: %v", err)
	}
}

func TestPluginLoadCannotFollowASymlinkOut(t *testing.T) {
	f := newPluginFixture(t)
	if err := os.Symlink(filepath.Join(f.root, "outside.scm"), filepath.Join(f.dir, "notes", "sneaky.scm")); err != nil {
		t.Skip("no symlinks")
	}
	path := f.file(t, "p.scm", `(plugin (name "p") (requires (fs (read "notes"))))`)
	p, err := OpenPlugin(context.Background(), path, Allow(FSRead(f.root)))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Interp.EvalString(context.Background(), `(load "notes/sneaky.scm")`); err == nil {
		t.Error("load followed a symlink out of the granted tree")
	}
	if _, ok := p.Interp.Global().Lookup("escaped"); ok {
		t.Error("a file outside the grant was loaded")
	}
}

func TestPluginsDoNotShareAnything(t *testing.T) {
	f := newPluginFixture(t)
	a := f.file(t, "a.scm", `(plugin (name "a")) (define shared 1)`)
	b := f.file(t, "b.scm", `(plugin (name "b")) (define other 2)`)
	pa, err := OpenPlugin(context.Background(), a, Full())
	if err != nil {
		t.Fatal(err)
	}
	defer pa.Close()
	pb, err := OpenPlugin(context.Background(), b, Full())
	if err != nil {
		t.Fatal(err)
	}
	defer pb.Close()
	if _, ok := pb.Interp.Global().Lookup("shared"); ok {
		t.Error("plugin b sees plugin a's definitions")
	}
}

func TestPluginHostAPIAndOutput(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "h.scm", `(plugin (name "h"))
(display "loaded ")
(define (go) (host-double 21))`)
	var out strings.Builder
	p, err := OpenPlugin(context.Background(), path, Allow(),
		PluginOutput(&out),
		PluginSetup(func(in *scheme.Interp) error {
			return in.Register(&scheme.Prim{Name: "host-double", Meta: scheme.Meta{Doc: "double", Params: []scheme.Param{{Name: "n", Type: "integer"}}},
				Handler: func(c *scheme.Call) (scheme.Value, error) {
					n, err := c.Int(0)
					return scheme.Int(n * 2), err
				}})
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if v, err := p.Call(context.Background(), "go"); err != nil || v.I != 42 || out.String() != "loaded " {
		t.Errorf("go = %v, %v; output %q", v, err, out.String())
	}
	if _, err := p.Call(context.Background(), "missing"); err == nil {
		t.Error("calling a missing procedure should fail")
	}
}

func TestPluginHostGateStillApplies(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "g.scm", `(plugin (name "g") (requires (rand)))
(define (roll) (rand-int 6))`)
	deny := denyPrim("rand-int")
	p, err := OpenPlugin(context.Background(), path, Allow(Module(Rand)), PluginGate(deny))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Call(context.Background(), "roll")
	var d *scheme.Denied
	if !errors.As(err, &d) {
		t.Errorf("the host's gate was bypassed: %v", err)
	}
}

type denyPrim string

func (d denyPrim) Before(_ context.Context, p *scheme.Prim, _ []scheme.Value) error {
	if p.Name == string(d) {
		return &scheme.Denied{Prim: p.Name, Reason: "host says no"}
	}
	return nil
}
func (denyPrim) RegisterChecker(string, string, scheme.Value) error { return errors.New("no") }

func TestManifestErrors(t *testing.T) {
	for src, want := range map[string]string{
		``:                                     "empty plugin",
		`(define x 1)`:                         "must start with a (plugin",
		`(plugin)`:                             "needs a (name",
		`(plugin (name ""))`:                   "needs a (name",
		`(plugin (name "x") (name "y"))`:       "appears twice",
		`(plugin (name "x") (colour "red"))`:   "unknown clause",
		`(plugin (name "x") (requires (ftp)))`: "unknown clause",
		`(plugin (name "x") (requires (fs)))`:  "needs (read",
		`(plugin (name (a b)))`:                "takes text",
		`(plugin (name "x")`:                   "",
	} {
		_, err := ParseManifest(src, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", src, err, want)
		}
	}
}

func TestManifestPlanShowsTheGrantWithoutRunningAnything(t *testing.T) {
	f := newPluginFixture(t)
	path := f.file(t, "risky.scm", `(plugin (name "risky") (requires (fs (write "notes")) (net (allow "evil.com"))))
(fs-write-text "notes/ran" "the plugin ran")`)
	m, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	granted, refused := m.Plan(Allow(FSRead(f.root), NetHosts("good.com")), "")
	notes := filepath.Join(f.dir, "notes")
	// The host would let it read its notes but not write them, and would not let it reach evil.com.
	if !equalStrings(granted.FS.Read, []string{notes}) || len(granted.FS.Write) != 0 || granted.Has(Net) || strings.Join(refused, ";") != "fs write "+notes+";net evil.com" {
		t.Errorf("granted %s, refused %v", granted, refused)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "notes", "ran")); err == nil {
		t.Error("reading the manifest ran the plugin")
	}
}
