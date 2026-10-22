# Embedding Brote in a Go program

Brote is a Scheme interpreter that a Go program owns. The program decides
what a Scheme procedure can *do* by registering **primitives**: Go functions
behind a name. Scheme decides *when*. Nothing else reaches the host.

```go
import "github.com/fclaude/brote/scheme"
```

The runnable examples in [scheme/example_test.go](../scheme/example_test.go)
are the quick start (`go doc github.com/fclaude/brote/scheme` lists them). The
language itself is [scheme.md](scheme.md). This page is the contract an
embedder relies on: each statement below is checked by a test named in the
last section, or says it is not.

## Lifecycle

```go
in := scheme.New(scheme.WithOutput(w), scheme.WithBaseDir(dir)) // builtins and prelude loaded
in.Register(&scheme.Prim{...})                                  // host operations
in.SetGate(g)                                                   // optional policy
in.Freeze()                                                     // optional: no more registration
v, err := in.EvalString(ctx, src)                               // run source; value of the last form
v, err = in.Apply(ctx, proc, args)                              // call a procedure value
```

- **Ownership.** An `*Interp` has no locking. Use it from one goroutine at a
  time: two goroutines evaluating in the same interpreter race on its
  environment (the race detector reports it; without it, Go's runtime can end
  the process with `concurrent map writes`). A host that receives work on many
  goroutines funnels it to one that owns the interpreter, or holds a mutex
  around every call. A handler may call back into the interpreter that called
  it, on the same goroutine (`Call.In.Apply(Call.Ctx, …)`), as `map` and
  `register-tool` do.
- **Separate interpreters share nothing.** There is no package-level state, so
  a host may run any number of them, each in its own goroutine.
- **Programs outlive evaluations.** Definitions stay in the global
  environment (`in.Global()`), and a closure value keeps its environment.
  A host can look one up (`in.Global().Lookup("on-save")`), keep the `Value`,
  and `Apply` it later with arguments it builds. `Global().Snapshot()` and
  `Restore` copy the bindings and put them back.
- **Registration is open until `Freeze`.** Names are unique for the life of
  the interpreter: a builtin cannot be shadowed and a primitive cannot be
  registered twice. After `Freeze`, `Register` and `(register-tool …)` fail.
  Use it to fix the set of operations before running code you did not write.
  `BeginReload`/`EndReload` re-run a program file over a frozen interpreter:
  `register-tool` replaces the body of a tool whose name and declaration are
  unchanged, and notes any other registration instead of applying it.

## Values

A `scheme.Value` is a struct passed by value. Its `Kind` says which field
holds the payload:

| Kind | Field | Make one with |
|---|---|---|
| `KNil` `KVoid` | | `scheme.Nil`, `scheme.Void` |
| `KBool` | `I` (1 or 0) | `scheme.Bool(b)`, `scheme.True`, `scheme.False` |
| `KInt` `KFloat` | `I`, `F` | `scheme.Int(n)`, `scheme.Float(f)` |
| `KString` `KSymbol` | `S` | `scheme.Str(s)`, `scheme.Sym(s)` |
| `KPair` | `P.Car`, `P.Cdr` | `scheme.Cons(a, d)`, `scheme.List(vs...)` |
| `KVec` | `V.Items` | `scheme.Vec(vs...)` |
| `KProc` `KPrim` | `Proc`, `Prim` | a program's `lambda`; `Register` |
| `KHandle` | `H` | `scheme.HandleValue(&scheme.Handle{...})` |

`v.Slice()` returns a proper list's elements. `scheme.Write(v)` is the
readable form (strings quoted), `scheme.Display(v)` the human form. Strings
are UTF-8, immutable, and indexed by rune. `scheme.FromJSON` and `ToJSON`
convert JSON text and values (`scheme.md` §5 has the mapping).

Source is read with `scheme.Read` (every top-level form) or `ReadOne`.

## Host operations

```go
in.Register(&scheme.Prim{
    Name: "buffer-text",
    Meta: scheme.Meta{
        Doc: "The text of lines START to END of the current buffer.",
        Params: []scheme.Param{
            {Name: "start", Type: "integer", Doc: "first line, 1-based"},
            {Name: "end", Type: "integer", Doc: "last line", Optional: true},
        },
    },
    Handler: func(c *scheme.Call) (scheme.Value, error) {
        start, err := c.Int(0)
        if err != nil {
            return scheme.Value{}, err
        }
        // c.Args has fewer elements when an optional parameter was left out.
        return scheme.Str(text), nil
    },
})
```

- `Doc` is required: `Register` refuses a primitive without one. Brote's own
  suite also checks that every builtin is in [scheme.md](scheme.md); a host
  with a reference of its own can do the same (see `TestDocCoverage`).
- The interpreter checks the argument count against `Params` and `Variadic`
  before the handler runs. The handler checks types: `c.String(i)`,
  `c.Int(i)`, `c.Number(i)`, `c.List(i)`, `c.Pair(i)`, `c.Proc(i)` and
  `c.Symbol(i)` return the argument or a type error that names the primitive
  and the argument.
- Parameter `Type` is documentation and the shape of the tool schema
  (`Prim.Schema()`); it is not enforced. The handler is where a type is
  enforced.
- A handler returns a value or an error. An ordinary error becomes a
  `*scheme.Error` whose message is `name: text` and whose `Cause` is the
  error the handler returned (`errors.Is` and `errors.As` see through it);
  `guard` in the program can catch it.
- A handler receives the evaluation's `Ctx`. A handler that blocks (waits for
  a person, a network reply, a subprocess) honours it, since cancellation
  only stops Scheme between steps, not a Go call in progress.
- The result of a handler is tagged with whatever provenance the primitive
  declares (`Meta.TaintSource`) and its arguments carry (`scheme.md` §5).

## Errors

| Error | Raised for | Caught by `guard`? |
|---|---|---|
| `*scheme.ParseError` | source that does not read (`Line`, `Col`, `Msg`) | n/a: nothing was evaluated |
| `scheme.ErrIncomplete` | source that stops short: an open list or string | n/a. A REPL reads another line |
| `*scheme.Error` | `(error …)` and every runtime failure | yes |
| `*scheme.Denied` | a gate refused a primitive | **no** |
| `*scheme.Halt` | a handler ended the work | **no** |
| `context.Canceled`, `context.DeadlineExceeded` | the evaluation's context is done | **no** |

`scheme.Catchable(ctx, err)` is the rule `guard` applies; a handler that
catches errors from `Apply` itself (to try the next file, say) should use it
so as not to swallow a cancellation. Errors from a file loaded with `LoadFile`
carry the path in front.

## Cancellation and limits

- The context is checked at every evaluation step, not only at primitive
  calls: `(let loop () (loop))` stops at its deadline. The interpreter is
  usable afterwards, with the definitions it had.
- Evaluations nested inside each other are bounded by `scheme.MaxDepth`
  (10,000), as are the reader, the printer, `equal?` and `json-emit`. Past it
  the program gets an ordinary error. Tail calls are not nested.
- Brote does not meter memory. A program can build a very large list or
  string within its time; a host that runs programs it does not trust bounds
  the process. (`iota` alone is capped, at 1,000,000.)
- A cancelled or failed evaluation leaves its side effects: the definitions
  it made and whatever its handlers did. A host that needs all or nothing
  makes its handlers record intent and applies it after the evaluation
  succeeded.

## Output

`display`, `write` and `newline` go to the writer given to `WithOutput`
(`io.Discard` by default). It is called from the evaluating goroutine.

## Files and trust

`in.LoadFile(ctx, path)` evaluates a file in the global environment, with a
relative path resolved against `WithBaseDir`, and refuses a file that loads
itself. The builtin `(load path)` does the same for a program. The
interpreter evaluates whatever it is handed; **which files to load is the
host's decision.** Do not load a file only because it is in a directory the
user opened.

`load` is the one builtin that touches the operating system. It is declared
`Host` and `Hidden`; with no gate a program can load any file the process can
read. A host that does not want that installs a gate (below).

## Gates and origin

A `Gate` is the host's policy hook:

```go
type Gate interface {
    Before(ctx context.Context, p *Prim, args []Value) error
    RegisterChecker(name, verdict string, predicate Value) error
}
```

`Before` runs at the single place where Scheme calls Go, for every primitive
application: from the program, the prelude, a loaded file, or `Apply`, and
through `map`, `apply` and `sort`. Return `nil` to allow, or an error to
refuse (conventionally `&scheme.Denied{…}`, which `guard` cannot catch). It
may block, for instance to ask the person, and must honour `ctx`. It sees the
primitive's `Meta` (`Host`, `Irreversible`, `Egress`, the parameters' `Role`s)
and the evaluated arguments with their provenance. `SetGate(nil)` allows
everything.

`RegisterChecker` is called by the hidden builtin `register-check`; a gate
with no use for it returns an error. The interpreter calls it only for an
evaluation whose `OriginFrom(ctx)` is `OriginUser`.

The origin travels in the context: `scheme.WithOrigin(ctx, scheme.OriginUser)`.
A context that says nothing is `OriginHarness`, never the user's. The
interpreter attaches one meaning to it (`register-check` above); a gate may
attach more, such as refusing `wipe` to a program a model wrote.

`SetExpand` installs a rewrite applied to arguments whose role is `path`,
`source` or `argv` before the gate sees them, so the gate and the handler
judge the same string.

## What is Nodwyn's

Brote was written inside Nodwyn, a coding agent, and its API still carries
what that host needed. An embedder can ignore all of it; nothing here is
required to register a primitive or run a program.

- `Meta.Egress*`, `ConfirmReason` and `Preview`, `Param.Role` and `Items`,
  `Prim.SkipsAsVoid`: declarations for Nodwyn's gate.
- `Prim.Tool`, `Interp.Tools`, `Prim.Schema`, `ArgsFromJSON`, `TidyArgs`,
  `ToolSchema`: presenting primitives as tools for a language model.
- `Taint`, `TaintOf`, `Union`, `Tainted`, `DeepTaint`, `CredTag`, the `Tag*`
  constants and `Handle`: provenance and credential handles. The builtins
  `taint-of`, `tainted?`, `wipe` and `handle?` exist for them.
- `Origin`, `Halt`, `BeginReload`/`EndReload`, `Freeze`.
- Some error and doc strings cite Nodwyn's ADRs ("ADR 9"); the comments that
  say "SPEC", "L2" or "P6" refer to Nodwyn's design record, not to a Brote
  document.

A second host now shows which of them an embedder needs. Coigüe, a terminal
editor, embeds Brote for its users' commands and uses `New`, `WithOutput`,
`Register`, `SetGate` (to refuse what a command must not do), `Freeze`,
`EvalString` and `Apply`, the `Prim`, `Meta`, `Param`, `Call` and `Value`
types with their constructors, and `Denied`. It uses none of the groups above:
no tools, provenance, credential handles, origin, halt or reload. Of the 91
exported names, Nodwyn uses 78 and Coigüe 33 (counted by name in each
program's non-test code), and 31 are common to both. So the API has two
layers, and an embedder needs only the first:

- **Core:** the interpreter and its lifecycle, values, primitives and `Call`,
  errors, cancellation, `WithOutput`, `WithBaseDir`, `LoadFile`, and the gate
  (`SetGate`, `Gate`, `Denied`) as the one place policy is applied.
- **Provenance and presentation, Nodwyn's today:** the groups listed above.

They keep their behaviour in v0.1.0, so that extracting Brote changed nothing
for Nodwyn. Moving the second layer behind narrower interfaces would break
Nodwyn's imports, so it waits for a minor version.

## What the tests check

| Statement | Test |
|---|---|
| Language behaviour and the documented builtins | `TestConformance`, `TestDocCoverage` (`scheme/scheme_test.go`) |
| Every primitive application crosses the gate once | `TestGateIsTheSingleExit` |
| Denial and cancellation pass `guard` | `TestGuardCannotCatchDenialOrCancellation` |
| A halt passes `guard` | `TestHaltPassesThroughGuard` (`scheme/embed_test.go`) |
| Cancellation needs no primitive; the interpreter survives it | `TestCancellationNeedsNoPrimitive`, `TestInterpSurvivesCancellation` |
| Interpreters are independent | `TestInterpsAreIndependent` (run with `-race`) |
| A handler may call back into Scheme | `TestHandlerCallsBackIntoScheme` |
| Handler errors reach the program and the host | `TestHandlerErrorsReachProgramAndHost` |
| Registration ends at `Freeze` | `TestFreezeEndsRegistration` |
| An unsaid origin is not the user's | `TestAnUnsaidOriginIsNotTheUsers`, `TestOriginDefaultsToNotTheUser` |
| Concurrent use of **one** interpreter is unsafe | not tested (a test that fails by design); observed once under `-race` on 2026-10-22 |
