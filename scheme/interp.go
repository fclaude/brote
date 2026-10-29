package scheme

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

//go:embed all:prelude
var preludeFS embed.FS

// specialForms is the fixed set of reserved names: every one is parsed as
// a special form wherever it heads a list, whatever the environment binds.
// There is no API to add to it.
var specialForms = []string{
	"define", "lambda", "if", "let", "let*", "letrec", "begin", "set!",
	"quote", "quasiquote", "unquote", "unquote-splicing",
	"cond", "case", "else", "=>", "and", "or",
	"do", "when", "unless", "guard", "with-output-to-string",
	"define-syntax", "let-syntax", "letrec-syntax", "syntax-rules",
}

// Interp is one interpreter instance: a global environment, the primitive
// table, and the guard hook.
//
// An Interp can be used from several goroutines: its environments, vectors
// and hash tables are locked, and each evaluation keeps its own state in a
// task. The exceptions are the host's own calls that change what the
// interpreter is (Register, Freeze, BeginReload, EndReload, SetGate,
// SetExpand): make those before evaluating, or between evaluations.
type Interp struct {
	global  *Env
	gate    Gate
	expand  func(string) string // rewrites path and argv arguments before the guard; nil for none
	out     io.Writer           // where output goes outside a capture; safe for concurrent use
	input   *port               // the current input port
	isSF    map[string]bool
	baseDir string

	// mu guards the primitive table and the fields that describe it.
	mu    sync.RWMutex
	prims map[string]*Prim
	// reloading is set while a session re-runs its init file: what would
	// change the tool list or the guard it started with is noted in
	// reloadNotes instead of applied (ADR 9).
	reloading   bool
	reloadNotes []string
	// frozen is set by Freeze, once the tools a session offers have been
	// registered: after it no primitive can be added (ADR 9).
	frozen bool

	// loadMu guards loading, the files LoadFile is evaluating, so a file that
	// loads itself, directly or through another, is refused.
	loadMu  sync.Mutex
	loading map[string]bool

	// tasks is non-nil when the host gave scripts tasks (WithTasks).
	tasks *taskRuntime
	// gateSem is held while a Gate that is not safe for concurrent use runs.
	gateSem chan struct{}
}

// MaxDepth is how deeply evaluation, and the reader, the printer, equal? and
// json-emit, may nest. Evaluation is counted per task. A nested Eval takes one to five kilobytes of Go stack
// (more when it recurses through a primitive such as map), so the limit costs
// at most some 50 MB, far inside Go's gigabyte even with sub-agents nested
// above it. Tail calls do not nest, and the default loop's own walks are tail
// calls, so only a recursion over thousands of items that is not one meets it.
const MaxDepth = 10_000

// BeginReload starts re-running an init file over this interpreter:
// register-tool replaces the body of a tool whose name, description and
// parameters are unchanged, and notes any other registration instead of
// applying it; register-check is noted, not applied.
func (in *Interp) BeginReload() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.reloading, in.reloadNotes = true, nil
}

// EndReload ends it and returns what could not change in this session.
func (in *Interp) EndReload() []string {
	in.mu.Lock()
	defer in.mu.Unlock()
	notes := in.reloadNotes
	in.reloading, in.reloadNotes = false, nil
	return notes
}

func (in *Interp) isReloading() bool {
	in.mu.RLock()
	defer in.mu.RUnlock()
	return in.reloading
}

func (in *Interp) noteReload(format string, args ...any) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.reloadNotes = append(in.reloadNotes, fmt.Sprintf(format, args...))
}

// Freeze ends registration: Register, and so register-tool, fail from here
// on. The loop calls it when it fixes the tool list, so a tool body that
// registers a helper later cannot add a model-callable primitive that was
// never offered.
func (in *Interp) Freeze() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.frozen = true
}

// Option configures New.
type Option func(*Interp)

// WithOutput sets where display/write/newline go. Default: io.Discard —
// the host decides who sees output (ADR 10).
func WithOutput(w io.Writer) Option { return func(in *Interp) { in.out = w } }

// WithBaseDir sets the directory relative load paths resolve against
// (the repository root in a session). Default: the process working dir.
func WithBaseDir(dir string) Option { return func(in *Interp) { in.baseDir = dir } }

// New creates an interpreter with the builtins and the prelude loaded.
func New(opts ...Option) *Interp {
	in := &Interp{
		global: NewEnv(nil),
		prims:  make(map[string]*Prim),
		out:    io.Discard,
		isSF:   make(map[string]bool, len(specialForms)),

		gateSem: make(chan struct{}, 1),
	}
	for _, s := range specialForms {
		in.isSF[s] = true
	}
	for _, o := range opts {
		o(in)
	}
	if in.input == nil {
		in.input = &port{input: true}
	}
	in.out = &syncWriter{w: in.out}
	in.registerBuiltins()
	if err := in.loadPrelude(); err != nil {
		panic("scheme: prelude: " + err.Error())
	}
	return in
}

func (in *Interp) loadPrelude() error {
	entries, err := fs.ReadDir(preludeFS, "prelude")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".scm") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		src, err := preludeFS.ReadFile("prelude/" + n)
		if err != nil {
			return err
		}
		if _, err := in.EvalLibrary(context.Background(), "prelude/"+n, string(src)); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	return nil
}

// Global returns the global environment.
func (in *Interp) Global() *Env { return in.global }

// EvalString reads and evaluates every form in src in the global
// environment and returns the last value (Void for empty input).
func (in *Interp) EvalString(ctx context.Context, src string) (Value, error) {
	return in.EvalNamed(ctx, "", src)
}

// EvalNamed is EvalString for text that came from the file or other source
// called name, which is what an error raised in it says it was in.
func (in *Interp) EvalNamed(ctx context.Context, name, src string) (Value, error) {
	var file *Source
	if name != "" {
		file = &Source{Name: name}
	}
	return in.evalSource(ctx, file, src)
}

// EvalLibrary is EvalNamed for Scheme that is part of the host and not of the
// script, such as the Scheme half of a module. An error raised inside it is
// reported where the script called it, and the procedures defined in it do not
// appear in a trace.
func (in *Interp) EvalLibrary(ctx context.Context, name, src string) (Value, error) {
	return in.evalSource(ctx, &Source{Name: name, Library: true}, src)
}

func (in *Interp) evalSource(ctx context.Context, file *Source, src string) (Value, error) {
	forms, err := readSource(file, src)
	if err != nil {
		return Value{}, err
	}
	t, ctx, done := in.enter(ctx)
	result := Void
	for _, f := range forms {
		result, err = in.eval(t, ctx, f, in.global)
		if err != nil {
			break
		}
	}
	if done != nil {
		err = done(err)
	}
	if err != nil {
		return Value{}, err
	}
	return result, nil
}

// LoadFile evaluates a file's forms in the global environment. A relative
// path resolves against the base directory (WithBaseDir).
func (in *Interp) LoadFile(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) && in.baseDir != "" {
		path = filepath.Join(in.baseDir, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		in.loadMu.Lock()
		if in.loading[abs] {
			in.loadMu.Unlock()
			return fmt.Errorf("%s: loaded again while it is being loaded", path)
		}
		if in.loading == nil {
			in.loading = map[string]bool{}
		}
		in.loading[abs] = true
		in.loadMu.Unlock()
		defer func() {
			in.loadMu.Lock()
			delete(in.loading, abs)
			in.loadMu.Unlock()
		}()
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := in.EvalNamed(ctx, path, string(src)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Apply calls a procedure with already-evaluated arguments. A handler that
// calls back into Scheme passes its Call.Ctx, which keeps the call in the
// handler's task.
func (in *Interp) Apply(ctx context.Context, f Value, args []Value) (Value, error) {
	// A call's frame takes its argument slice over, and the caller of Apply
	// still owns this one (a handler passes its Call.Args on).
	args = slices.Clone(args)
	t, ctx, done := in.enter(ctx)
	v, err := in.apply(t, ctx, f, args)
	if done != nil {
		err = done(err)
	}
	if err != nil {
		return Value{}, err
	}
	return v, nil
}

func (in *Interp) apply(t *task, ctx context.Context, f Value, args []Value) (Value, error) {
	switch f.Kind {
	case KPrim:
		return in.applyPrim(t, ctx, f.Prim, args)
	case KProc:
		env, err := bindParams(f.Proc, args)
		if err != nil {
			return Value{}, err
		}
		return in.evalBody(t, ctx, f.Proc, env)
	default:
		return Value{}, errorf("apply", "not a procedure: %s", Write(f))
	}
}

// evalBody evaluates the body of fn, the last expression in tail position of
// this Go frame.
func (in *Interp) evalBody(t *task, ctx context.Context, fn *Closure, env *Env) (Value, error) {
	return in.evalSeq(t, ctx, fn.Body, env, fn)
}

// evalSeq evaluates a sequence, in fn's body if fn is not nil.
func (in *Interp) evalSeq(t *task, ctx context.Context, body []Value, env *Env, fn *Closure) (Value, error) {
	for i := 0; i < len(body)-1; i++ {
		if _, err := in.evalIn(t, ctx, body[i], env, fn); err != nil {
			return Value{}, err
		}
	}
	return in.evalIn(t, ctx, body[len(body)-1], env, fn)
}

func bindParams(c *Closure, args []Value) (*Env, error) {
	n := len(c.Params)
	if len(args) < n || (len(args) > n && c.Rest == "") {
		name := c.DisplayName()
		if name == "" {
			name = "#<procedure>"
		}
		if c.Rest != "" {
			return nil, errorf(name, "expected at least %d argument(s), got %d", n, len(args))
		}
		return nil, errorf(name, "expected %d argument(s), got %d", n, len(args))
	}
	// The frame takes the argument slice over (nothing else uses it once the
	// call is made) and shares the closure's names. Nothing can see it until
	// the body runs, so it is built without its lock.
	if c.Rest != "" {
		args = append(args[:n:n], List(args[n:]...))
	}
	names := c.names
	if names == nil {
		names = frameNames(c) // a closure not made by makeClosure
	}
	return frameOf(c.Env, names, args), nil
}

// Eval evaluates x in env. Tail positions loop instead of recursing, so
// tail-recursive Scheme runs in constant Go stack (docs/scheme.md §3).
func (in *Interp) Eval(ctx context.Context, x Value, env *Env) (Value, error) {
	t, ctx, done := in.enter(ctx)
	v, err := in.eval(t, ctx, x, env)
	if done != nil {
		err = done(err)
	}
	if err != nil {
		return Value{}, err
	}
	return v, nil
}

func (in *Interp) eval(t *task, ctx context.Context, x Value, env *Env) (Value, error) {
	return in.evalIn(t, ctx, x, env, nil)
}

// evalIn is eval for x as part of the body of fn, or of no procedure if fn is
// nil. When x fails it tells the error where and, for a procedure, which: the
// form being evaluated when the loop below gave up is the one that failed.
func (in *Interp) evalIn(t *task, ctx context.Context, x Value, env *Env, fn *Closure) (v Value, err error) {
	var at *Pos // the last call in the script that the loop below followed into a procedure
	if t.depth++; t.depth > t.limit {
		t.depth--
		return Value{}, errorf("eval", "recursion too deep: more than %d nested evaluations", t.limit)
	}
	defer func() {
		t.leave()
		if err != nil {
			err = annotate(err, x, fn, at)
		}
	}()
	for {
		switch x.Kind {
		case KSymbol:
			v, ok := env.Lookup(x.S)
			if !ok {
				return Value{}, errorf("eval", "unbound variable: %s", x.S)
			}
			return v, nil
		case KNil:
			return Value{}, errorf("eval", "empty application ()")
		case KPair:
			// fallthrough to the form/application logic below
		default:
			return x, nil // self-evaluating
		}

		head := x.P.Car
		if head.Kind == KSymbol && in.isSF[head.S] {
			form, err := formArgs(x)
			if err != nil {
				return Value{}, err
			}
			switch head.S {
			case "quote":
				if len(form) != 1 {
					return Value{}, errorf("quote", "expected 1 argument")
				}
				return form[0], nil

			case "quasiquote":
				if len(form) != 1 {
					return Value{}, errorf("quasiquote", "expected 1 argument")
				}
				return in.quasi(t, ctx, form[0], env)

			case "unquote", "unquote-splicing":
				return Value{}, errorf(head.S, "outside quasiquote")

			case "else", "=>":
				return Value{}, errorf(head.S, "outside cond")

			case "if":
				if len(form) < 2 || len(form) > 3 {
					return Value{}, errorf("if", "expected 2 or 3 arguments")
				}
				cond, err := in.eval(t, ctx, form[0], env)
				if err != nil {
					return Value{}, err
				}
				if cond.IsTrue() {
					x = form[1]
				} else if len(form) == 3 {
					x = form[2]
				} else {
					return Void, nil
				}
				continue

			case "define":
				return in.evalDefine(t, ctx, form, env)

			case "set!":
				if len(form) != 2 || form[0].Kind != KSymbol {
					return Value{}, errorf("set!", "expected (set! name expr)")
				}
				v, err := in.eval(t, ctx, form[1], env)
				if err != nil {
					return Value{}, err
				}
				if !env.Set(form[0].S, v) {
					return Value{}, errorf("set!", "unbound variable: %s", form[0].S)
				}
				return Void, nil

			case "lambda":
				c, err := makeClosure("lambda", form, env)
				if err != nil {
					return Value{}, err
				}
				return Value{Kind: KProc, Proc: c}, nil

			case "begin":
				if len(form) == 0 {
					return Void, nil
				}
				for i := 0; i < len(form)-1; i++ {
					if _, err := in.eval(t, ctx, form[i], env); err != nil {
						return Value{}, err
					}
				}
				x = form[len(form)-1]
				continue

			case "let", "let*", "letrec":
				nx, nenv, err := in.evalLet(t, ctx, head.S, form, env)
				if err != nil {
					return Value{}, err
				}
				x, env = nx, nenv
				continue

			case "case":
				result, err := in.evalCase(t, ctx, form, env)
				if err != nil {
					return Value{}, err
				}
				if result.done {
					return result.value, nil
				}
				x = result.next
				continue

			case "do":
				return in.evalDo(t, ctx, form, env)

			case "when", "unless":
				who := head.S
				if len(form) == 0 {
					return Value{}, errorf(who, "expected a test")
				}
				test, err := in.eval(t, ctx, form[0], env)
				if err != nil {
					return Value{}, err
				}
				take := test.IsTrue()
				if who == "unless" {
					take = !take
				}
				if !take || len(form) < 2 {
					return Void, nil
				}
				for i := 0; i < len(form)-1; i++ {
					if _, err := in.eval(t, ctx, form[i], env); err != nil {
						return Value{}, err
					}
				}
				x = form[len(form)-1]
				continue

			case "guard":
				return in.evalGuard(t, ctx, form, env)

			case "define-syntax":
				return in.evalDefineSyntax(form, env)

			case "let-syntax", "letrec-syntax":
				nx, nenv, err := in.evalLetSyntax(head.S, form, env)
				if err != nil {
					return Value{}, err
				}
				x, env = nx, nenv
				continue

			case "syntax-rules":
				return Value{}, errorf("syntax-rules", "only valid in (define-syntax name (syntax-rules …))")

			case "with-output-to-string":
				if len(form) == 0 {
					return Value{}, errorf("with-output-to-string", "expected a body expression")
				}
				saved, savedTaint := t.out, t.printed
				buf := new(strings.Builder)
				t.out, t.printed = buf, Taint{}
				defer func() { t.printed = savedTaint }()
				var err error
				for _, f := range form {
					if _, err = in.eval(t, ctx, f, env); err != nil {
						break
					}
				}
				t.out = saved
				if err != nil {
					return Value{}, err
				}
				return Value{Kind: KString, S: buf.String(), Taint: t.printed}, nil

			case "cond":
				result, err := in.condClauses(t, ctx, form, env)
				if err != nil {
					return Value{}, err
				}
				if result.done {
					return result.value, nil
				}
				x = result.next
				continue

			case "and":
				if len(form) == 0 {
					return True, nil
				}
				for i := 0; i < len(form)-1; i++ {
					v, err := in.eval(t, ctx, form[i], env)
					if err != nil {
						return Value{}, err
					}
					if !v.IsTrue() {
						return v, nil
					}
				}
				x = form[len(form)-1]
				continue

			case "or":
				if len(form) == 0 {
					return False, nil
				}
				for i := 0; i < len(form)-1; i++ {
					v, err := in.eval(t, ctx, form[i], env)
					if err != nil {
						return Value{}, err
					}
					if v.IsTrue() {
						return v, nil
					}
				}
				x = form[len(form)-1]
				continue
			}
		}

		// Application.
		if err := ctx.Err(); err != nil {
			return Value{}, wrap("eval", err)
		}
		var f Value
		var err error
		if head.Kind == KSymbol {
			var ok bool
			if f, ok = env.Lookup(head.S); !ok {
				return Value{}, errorf("eval", "unbound variable: %s", head.S)
			}
			if f.Kind == KObject {
				if m := macroOf(f); m != nil {
					nx, err := in.expandUse(m, x)
					if err != nil {
						return Value{}, err
					}
					x = nx
					continue
				}
			}
		} else if f, err = in.eval(t, ctx, head, env); err != nil {
			return Value{}, err
		}
		argForms, ok := x.P.argSlice()
		if !ok {
			return Value{}, errorf("eval", "improper application %s", Write(x))
		}
		args := make([]Value, len(argForms))
		for i, a := range argForms {
			if args[i], err = in.eval(t, ctx, a, env); err != nil {
				return Value{}, err
			}
		}
		switch f.Kind {
		case KPrim:
			return in.applyPrim(t, ctx, f.Prim, args)
		case KProc:
			nenv, err := bindParams(f.Proc, args)
			if err != nil {
				return Value{}, err
			}
			body := f.Proc.Body
			if loc := x.P.loc; loc != nil && !loc.isLibrary() {
				at = loc
			}
			fn = f.Proc
			for i := 0; i < len(body)-1; i++ {
				if _, err := in.evalIn(t, ctx, body[i], nenv, fn); err != nil {
					return Value{}, err
				}
			}
			x, env = body[len(body)-1], nenv
			continue
		default:
			return Value{}, errorf("eval", "not a procedure: %s", Write(f))
		}
	}
}

func formArgs(x Value) ([]Value, error) {
	args, ok := x.P.argSlice()
	if !ok {
		return nil, errorf(Write(x.P.Car), "improper form %s", Write(x))
	}
	return args, nil
}
