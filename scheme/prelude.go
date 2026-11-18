package scheme

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"sync"
)

//go:embed all:prelude
var preludeFS embed.FS

// The embedded declarations are read and evaluated once. Nothing in this
// private image is ever given to Scheme code or to an embedding application.
var embeddedPrelude = sync.OnceValues(func() ([]preludeFile, error) {
	names, err := fs.Glob(preludeFS, "prelude/*.scm")
	if err != nil {
		return nil, err
	}
	var files []preludeFile
	for _, name := range names {
		src, err := preludeFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		f, err := preparePreludeFile(name, string(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		files = append(files, f)
	}
	return files, nil
})

type preludeFile struct {
	source      Source
	bindings    map[string]Value
	initializer string
}

// A file of declarations only needs a fresh copy of its bindings. If a future
// prelude runs expressions at top level, evaluate that entire file as before:
// initialization can depend on host options and on earlier files.
func preparePreludeFile(name, src string) (preludeFile, error) {
	f := preludeFile{source: Source{Name: name, Library: true}}
	forms, err := readSource(&f.source, src)
	if err != nil {
		return f, err
	}
	for _, form := range forms {
		if !preludeDeclaration(form) {
			f.initializer = src
			return f, nil
		}
	}
	compiler := &Interp{global: NewEnv(nil), isSF: specialFormNames()}
	for _, form := range forms {
		if _, err := compiler.Eval(context.Background(), form, compiler.global); err != nil {
			return f, err
		}
	}
	f.bindings = compiler.global.Snapshot()
	return f, nil
}

func preludeDeclaration(v Value) bool {
	form, proper := v.Slice()
	if !proper || len(form) < 3 || form[0].Kind != KSymbol {
		return false
	}
	switch form[0].S {
	case "define-syntax":
		return true // syntax-rules is parsed, never evaluated as user code
	case "define":
		if form[1].Kind == KPair { // procedure shorthand
			return true
		}
		rhs := form[2]
		return len(form) == 3 && rhs.Kind == KPair && rhs.P.Car.Kind == KSymbol &&
			(rhs.P.Car.S == "lambda" || rhs.P.Car.S == "quote")
	default:
		return false
	}
}

func (in *Interp) loadPrelude() error {
	files, err := embeddedPrelude()
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := file.install(in); err != nil {
			return fmt.Errorf("%s: %w", file.source.Name, err)
		}
	}
	return nil
}

func (f *preludeFile) install(in *Interp) error {
	if f.bindings == nil {
		_, err := in.EvalLibrary(context.Background(), f.source.Name, f.initializer)
		return err
	}
	local := preludeCopy{source: f.source}
	for name, v := range f.bindings {
		switch {
		case v.Kind == KProc:
			proto := v.Proc
			c := &Closure{Name: proto.Name, Rest: proto.Rest, Params: slices.Clone(proto.Params),
				Doc: proto.Doc, Body: local.values(proto.Body), Env: in.global}
			c.names = frameNames(c)
			if n := proto.named.Load(); n != nil {
				c.nameIfAnonymous(*n)
			}
			v.Proc = c
		case macroOf(v) != nil:
			m := *macroOf(v)
			// The private pattern-variable and binder maps are read-only after
			// parsing. Each interpreter gets its own pattern and template pairs.
			m.rules = slices.Clone(m.rules)
			for i := range m.rules {
				r := &m.rules[i]
				r.pattern = local.datum(r.pattern)
				r.template = local.datum(r.template)
			}
			v = ObjectValue(&m)
		default:
			v = local.datum(v)
		}
		in.global.Define(name, v)
	}
	return nil
}

type preludeCopy struct {
	source Source
	cells  []Pair
}

func (c *preludeCopy) values(src []Value) []Value {
	out := make([]Value, len(src))
	for i, v := range src {
		out[i] = c.datum(v)
	}
	return out
}

// Reader data are trees of scalars, pairs and vectors. Copy the list spine
// iteratively. A new cell starts with empty argument and macro expansion caches.
func (c *preludeCopy) datum(v Value) Value {
	var out Value
	tail := &out
	for v.Kind == KPair {
		if len(c.cells) == 0 {
			c.cells = make([]Pair, 64)
		}
		p := &c.cells[0]
		c.cells = c.cells[1:]
		p.Car = c.datum(v.P.Car)
		if at := v.P.loc; at != nil {
			p.loc = &Pos{Source: &c.source, Line: at.Line, Col: at.Col}
		}
		*tail = Value{Kind: KPair, P: p}
		tail = &p.Cdr
		v = v.P.Cdr
	}
	if v.Kind == KVec {
		v = Vec(c.values(v.V.Elements())...)
	}
	*tail = v
	return out
}
