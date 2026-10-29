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

Evaluating from several goroutines at once is safe. Environments, vectors and hash tables are locked, so goroutines that share an `*Interp` (or a closure, or a table) cannot corrupt it or end the process, and every operation on a shared value is atomic. A *sequence* of operations is not: `(set! n (+ n 1))` read `n`, then wrote it, and another goroutine's write in between is lost, as it would be in Go. `hash-update!` is atomic, and calls its procedure with no lock held, so the procedure may run more than once and should have no effects of its own. Output from `display` reaches the writer one call at a time, so a writer that is not safe for concurrent use is fine. `set-car!` and `set-cdr!` are the exception that proves the rule: pairs are read without a lock, so Brote changes one only while it is the only evaluation running on the interpreter and no task is alive, and refuses otherwise with an error (starting an evaluation or a task waits for a change in progress). Each `EvalString`, `Apply` or `Eval` that is not called from a handler is its own evaluation, with its own recursion depth against `MaxDepth` and its own `with-output-to-string` capture.

What is not safe to do while an evaluation runs is change what the interpreter *is*: `Register`, `Freeze`, `BeginReload`, `EndReload`, `SetGate`, `SetExpand`, and replacing a registered primitive's `Handler`. Make those before evaluating, or between evaluations. Your handlers and your `Gate` are called from whichever goroutine is evaluating, so they must be safe for that if you evaluate from more than one. A handler may call back into the interpreter that called it (`Call.In.Apply(Call.Ctx, …)`), as `map` and `register-tool` do; pass `Call.Ctx` on, because it is what keeps the callback in the evaluation that made the call (a callback on a fresh context is a new evaluation, with a fresh depth count and no cancellation).

`Vector.Items` is no longer exported. `Len`, `At`, `SetAt` and `Elements` take the vector's lock.

Separate interpreters share nothing. There is no package-level state, so a host may run any number of them, each in its own goroutine.

Programs outlive evaluations. Definitions stay in the global environment (`in.Global()`), and a closure value keeps its environment. A host can look one up (`in.Global().Lookup("on-save")`), keep the `Value`, and `Apply` it later with arguments it builds. `Global().Snapshot()` and `Restore` copy the bindings and put them back.

Registration is open until `Freeze`. Names are unique for the life of the interpreter: a builtin can't be shadowed and a primitive can't be registered twice. After `Freeze`, `Register` and `(register-tool …)` fail, so use it to fix the set of operations before running code you didn't write. `BeginReload`/`EndReload` re-run a program file over a frozen interpreter: `register-tool` replaces the body of a tool whose name and declaration are unchanged, and notes any other registration instead of applying it.

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
| `KHash` | `Hash` | `scheme.NewHash()`; fill it from Scheme with `hash-set!` |
| `KProc` `KPrim` | `Proc`, `Prim` | a program's `lambda`; `Register` |
| `KHandle` | `H` | `scheme.HandleValue(&scheme.Handle{...})` |

`v.Slice()` returns a proper list's elements. `scheme.Write(v)` is the
readable form (strings quoted), `scheme.Display(v)` the human form. Strings
are UTF-8, immutable, and indexed by rune. `scheme.FromJSON` and `ToJSON`
convert JSON text and values (`scheme.md` §5 has the mapping).

A hash table is a pointer to shared storage, like a vector. Its entries stay in the order they were added, and a Go host reads them by evaluating `hash->alist` (or `hash-keys`) and walking the list. `DeepTaint` follows keys and values.

Source is read with `scheme.Read` (every top-level form) or `ReadOne`.

The `brote` command registers two more procedures for scripts, `command-line` and `read-file`, and gives the interpreter its standard input with `scheme.WithInput(r)`: that is the current input port, which `read-line`, `read-char`, `read-string` and `read` read when they are not given a port. They're part of the command (`cmd/brote/host.go`), so an interpreter you create with `scheme.New` has no input (reading finds the end at once) and neither of the two procedures. See the [command page](command.md) for what they do.

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
  only stops Scheme between steps, and it can't interrupt a Go call that's already running.
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

A `*scheme.Error` also says where it was raised: `Pos` is the line and column
of the form that failed (`Pos.String()` is `file:line:col`), `Trace` the
procedures it left, innermost first, and `Elided` how many more there were than
the sixteen `Trace` keeps. `Error()` is unchanged, so a host that matches on the
text keeps working; `scheme.FormatError(err)` writes the position, the message and
the trace for a person, and is what the `brote` command prints. `Pos` is nil for
a form the program built rather than read.

Text read with `EvalString` or `Read` has line and column but no file name. To
have positions name a file, evaluate with `EvalNamed(ctx, name, src)` (which
`LoadFile` does) or read with `ReadNamed(name, src)`. `EvalLibrary(ctx, name, src)`
is `EvalNamed` for Scheme that is part of the host and not of the script, as in
a module of `std`: an error raised in it is reported at the script's call of it.

## Tasks

`scheme.WithTasks(scheme.TaskLimits{MaxTasks: 64, MaxDepth: 2000})` gives scripts `spawn`, channels, `select`, `pmap` and the rest ([the language reference](scheme.md) lists them). Without it those names are unbound and nothing here applies. The `brote` command turns tasks on.

It is your statement that your handlers are safe to call from several goroutines at once: tasks run in parallel, so two tasks can be inside the same handler. `std`'s are. A handler that keeps state it does not guard itself is wrapped, `Handler: scheme.Serialize(h)`, which runs it one call at a time; do not wrap one that calls back into Scheme (`Call.In.Apply`), since a callback that waits for a task that is waiting for the handler would never finish.

- **The gate.** `Gate.Before` is called one call at a time, whatever the tasks do, so a gate written for one goroutine, including one that stops to ask the person, needs no change. A task waiting its turn still stops with its context. A gate that is safe to call concurrently says so by implementing `scheme.ConcurrentGate` (an extra method, `ConcurrentSafe()`), and is then called without the lock.
- **Origin and context.** A task's context is derived from its spawner's, so it has the same `Origin` and is cancelled with it. `Call.Ctx` is the calling task's context: a handler that honours it stops when its task is cancelled or its `with-timeout` ends, as `http-request`, `proc-run` and `time-sleep` do.
- **Lifetime.** Tasks belong to the evaluation that started them, the outermost `EvalString`, `Apply` or `Eval`. When it returns, tasks still running are cancelled and waited for. A task that failed and that nothing waited for fails the evaluation. A denial or a halt in any task ends the evaluation, whatever the script does with it.
- **Limits.** `MaxTasks` (default 256) bounds the tasks alive at once, across evaluations, and `spawn` past it raises an error. `MaxDepth` (default 2,500) is the nesting limit inside a spawned task; the task that started the evaluation keeps `scheme.MaxDepth`. A task is a goroutine and its stack, so these two bound what tasks can cost.
- **Several evaluations at once.** Each call into the interpreter from outside is its own evaluation with its own tasks. Deadlock detection assumes the tasks of the interpreter are the only things that wake each other: a task waiting on a channel that a *different* evaluation will send to later looks deadlocked when every task is waiting.
- **Audit.** `std.WithAudit`'s callback is called one event at a time.

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

Package `std` has the batteries (files, HTTP, programs, the environment, the clock, regular expressions, encodings), a policy that chooses which a program gets and how much of each, and `Runtime.Gate`, a gate that holds `load` to the files the program may read. Read [capabilities](capabilities.md) before you run code you did not write.

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
- Some error and doc strings cite design decisions from the agent Brote came out of, and
  the comments that say "SPEC", "L2" or "P6" refer to that agent's design record. None of that is a Brote document.

A second host now shows which of them an embedder needs. Coigüe, a terminal
editor, embeds Brote for its users' commands and uses `New`, `WithOutput`,
`Register`, `SetGate` (to refuse what a command must not do), `Freeze`,
`EvalString` and `Apply`, the `Prim`, `Meta`, `Param`, `Call` and `Value`
types with their constructors, and `Denied`. It uses none of the groups above:
no tools, provenance, credential handles, origin, halt or reload. Of the 91
exported names, Nodwyn uses 78 and Coigüe 33 (counted by name in each
program's non-test code), and 31 are common to both. So the API has two
layers, and an embedder needs only the first. The first layer is the interpreter and its lifecycle, values, primitives and `Call`, errors, cancellation, `WithOutput`, `WithBaseDir`, `LoadFile`, and the gate (`SetGate`, `Gate`, `Denied`) as the one place policy is applied. The second is the groups listed above, which Nodwyn uses today.

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
| One interpreter may be used from several goroutines | `scheme/concurrent_test.go`, under `-race` |
