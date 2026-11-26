package scheme

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// module is a file evaluated by import: once per interpreter, in an
// environment of its own under the global one, so what it defines and does
// not export stays its own.
type module struct {
	path  string // absolute
	env   *Env
	ready chan struct{} // closed once the file has been evaluated, or has failed
	err   error         // why it failed; set before ready is closed

	// exports is what (export name …) declared, in order; explicit says the
	// file used export at all. Only the evaluation of the file writes them.
	exports  []string
	explicit bool
}

// names is what the module exports: what it declared, or else every name it
// defined, sorted.
func (m *module) names() []string {
	if m.explicit {
		return m.exports
	}
	names := make([]string, 0)
	for n := range m.env.Snapshot() {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// fileFrame is what an evaluation knows of the file it is in, kept in its
// context: the file, and the module when the file is being imported. The
// frames of the files that loaded or imported it come after it.
type fileFrame struct {
	file   string  // absolute path
	mod    *module // nil for a file run with load or LoadFile
	parent *fileFrame
}

type fileFrameKey struct{}

func withFile(ctx context.Context, file string, mod *module) context.Context {
	parent, _ := ctx.Value(fileFrameKey{}).(*fileFrame)
	return context.WithValue(ctx, fileFrameKey{}, &fileFrame{file: file, mod: mod, parent: parent})
}

func fileFrameOf(ctx context.Context) *fileFrame {
	f, _ := ctx.Value(fileFrameKey{}).(*fileFrame)
	return f
}

// importPath is the file (import path) reads when it is evaluated under ctx:
// a relative path is taken against the directory of the file being loaded or
// imported, if there is one, and is otherwise returned as it is (the
// interpreter then takes it against its base directory, WithBaseDir). The
// gate is shown this path, so it judges the file that will be read.
func importPath(ctx context.Context, path string) string {
	if f := fileFrameOf(ctx); f != nil && !filepath.IsAbs(path) {
		return filepath.Join(filepath.Dir(f.file), path)
	}
	return path
}

// importPrim describes import's reading of a file to the gate and to the
// rewrite of paths (SetExpand), as load's own description does for load. It
// is not in the primitive table: import is a special form.
var importPrim = &Prim{
	Name: "import",
	Meta: Meta{
		Doc:    "Read a Scheme file for import.",
		Params: []Param{{Name: "path", Type: "string", Doc: "file to import", Role: "source"}},
		Host:   true,
		Hidden: true,
		Origin: "builtin",
	},
}

// evalImport is (import file name … (prefix p)).
func (in *Interp) evalImport(t *task, ctx context.Context, form []Value, env *Env) (Value, error) {
	if len(form) == 0 {
		return Value{}, errorf("import", `expected (import "file.scm" name … (prefix p))`)
	}
	if form[0].Kind == KPair {
		return Value{}, errorf("import", `Brote imports files, not libraries: (import "lib/util.scm"), got %s`, Write(form[0]))
	}
	pv, err := in.eval(t, ctx, form[0], env)
	if err != nil {
		return Value{}, err
	}
	if pv.Kind != KString {
		return Value{}, errorf("import", "expected a file name (a string), got %s %s", pv.Kind, Write(pv))
	}
	var only []string
	prefix := ""
	for _, s := range form[1:] {
		if s.Kind == KSymbol {
			only = append(only, s.S)
			continue
		}
		if items, ok := s.Slice(); ok && len(items) == 2 && items[0].Kind == KSymbol && items[0].S == "prefix" && items[1].Kind == KSymbol {
			prefix = items[1].S
			continue
		}
		return Value{}, errorf("import", "expected a name or (prefix p), got %s", Write(s))
	}
	m, err := in.importModule(t, ctx, pv)
	if err != nil {
		return Value{}, err
	}
	exports := m.names()
	if only == nil {
		only = exports
	}
	for _, n := range only {
		if !slices.Contains(exports, n) {
			return Value{}, errorf("import", "%s does not export %s (it exports: %s)", pv.S, n, strings.Join(exports, " "))
		}
	}
	for _, n := range only {
		v, _ := m.env.Lookup(n)
		env.Define(prefix+n, v)
	}
	return Void, nil
}

// importModule is the module for the file path names: evaluated now, or
// already, once for the interpreter. One that failed is forgotten, so a later
// import tries again.
func (in *Interp) importModule(t *task, ctx context.Context, pv Value) (*module, error) {
	args := in.expandArgs(importPrim, []Value{Str(importPath(ctx, pv.S))})
	if in.gate != nil {
		if err := in.callGate(ctx, importPrim, args); err != nil {
			return nil, wrap("import", err)
		}
	}
	path := args[0].S
	if !filepath.IsAbs(path) && in.baseDir != "" {
		path = filepath.Join(in.baseDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errorf("import", "%s: %v", pv.S, err)
	}
	var chain []string
	for f := fileFrameOf(ctx); f != nil; f = f.parent {
		if f.mod == nil {
			continue
		}
		chain = append(chain, f.file)
		if f.file == abs {
			slices.Reverse(chain)
			return nil, errorf("import", "import cycle: %s -> %s", strings.Join(chain, " -> "), abs)
		}
	}

	in.loadMu.Lock()
	m := in.modules[abs]
	if m != nil {
		in.loadMu.Unlock()
		select {
		case <-m.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if m.err != nil {
			return nil, m.err
		}
		return m, nil
	}
	m = &module{path: abs, env: NewEnv(in.global), ready: make(chan struct{})}
	if in.modules == nil {
		in.modules = map[string]*module{}
	}
	in.modules[abs] = m
	in.loadMu.Unlock()

	m.err = in.evalModule(t, withFile(ctx, abs, m), m, pv.S, path)
	if m.err != nil {
		in.loadMu.Lock()
		delete(in.modules, abs)
		in.loadMu.Unlock()
	}
	close(m.ready)
	if m.err != nil {
		return nil, m.err
	}
	return m, nil
}

// evalModule reads the module's file and evaluates its forms in its own
// environment, then checks that it defines what it exports.
func (in *Interp) evalModule(t *task, ctx context.Context, m *module, given, path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return errorf("import", "%s: %v", given, err)
	}
	forms, err := readSource(&Source{Name: path}, string(src))
	if err != nil {
		return err
	}
	for _, f := range forms {
		if _, err := in.eval(t, ctx, f, m.env); err != nil {
			return err
		}
	}
	defined := m.env.Snapshot()
	for _, n := range m.exports {
		if _, ok := defined[n]; !ok {
			return errorf("import", "%s exports %s, which it does not define", given, n)
		}
	}
	return nil
}

// evalExport is (export name …): it declares what the module being imported
// gives the file that imports it, and is only meaningful at its top level.
func (in *Interp) evalExport(ctx context.Context, form []Value, env *Env) (Value, error) {
	f := fileFrameOf(ctx)
	if f == nil || f.mod == nil || f.mod.env != env {
		return Value{}, errorf("export", "belongs at the top level of a file that is imported, with import")
	}
	for _, n := range form {
		if n.Kind != KSymbol {
			return Value{}, errorf("export", "expected names, got %s", Write(n))
		}
		if !slices.Contains(f.mod.exports, n.S) {
			f.mod.exports = append(f.mod.exports, n.S)
		}
	}
	f.mod.explicit = true
	return Void, nil
}
