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
- **Lifetime.** Tasks belong to the evaluation that started them, the outermost `EvalString`, `Apply` or `Eval`. When it returns, tasks still running are cancelled and waited for. A task that failed and that nothing waited for fails the evaluation. A denial or a halt in any task ends the evaluation, whatever the script does with it. The exception is a *detached* task, which a host grants separately ([below](#detached-tasks)).
- **Limits.** `MaxTasks` (default 256) bounds the tasks alive at once, across evaluations, and `spawn` past it raises an error. `MaxDepth` (default 2,500) is the nesting limit inside a spawned task; the task that started the evaluation keeps `scheme.MaxDepth`. A task is a goroutine and its stack, so these two bound what tasks can cost.
- **Several evaluations at once.** Each call into the interpreter from outside is its own evaluation with its own tasks. Deadlock detection assumes the tasks of the interpreter are the only things that wake each other: a task waiting on a channel that a *different* evaluation will send to later looks deadlocked when every task is waiting. A channel the *host* sends to is declared with `scheme.HostFed()`, [below](#host-fed-channels-and-timers).
- **Audit.** `std.WithAudit`'s callback is called one event at a time.

### Detached tasks

Coigüe-style hosts run Scheme in short synchronous commands on their UI goroutine and want work that continues after the command returns. That is a second grant:

```go
in := scheme.New(
    scheme.WithTasks(scheme.TaskLimits{MaxTasks: 64, Background: true}),
    scheme.WithTaskFailed(func(t scheme.Task, err error) { /* tell the user */ }),
)
defer in.Shutdown(ctx)
```

With `Background`, scripts get `(spawn-detached thunk)` and `(after-detached seconds)`. A detached task belongs to the interpreter, not to an evaluation:

- **Its handle works from later evaluations**: `task-wait`, `task-done?` and `task-cancel!` on it from a command that did not start it all work.
- **It counts against `MaxTasks`**, like any task, and has the same `MaxDepth`.
- **Its context descends from the spawner's by value, not by cancellation.** Everything the host put in the context of the evaluation that spawned it (`Origin`, which module's code is running, ...) is visible to its handlers through `Call.Ctx`, but ending the evaluation, or cancelling its context, or its `with-timeout`, does not end the task. It ends when it finishes, when a script `task-cancel!`s it, or at `Shutdown`.
- **It is its own evaluation inside.** Tasks it spawns with `spawn`, `pmap` and the rest end with it, and a denial or a halt in one of them is the detached task's error.
- **A failure no one waited for does not fail some unrelated later evaluation.** It goes to `WithTaskFailed`, called on the failed task's own goroutine, after the task has finished and outside every lock of the interpreter. The error may be a `*scheme.Denied`: that is how a host hears of a refused call made in the background. The hook is *not* called for a task that was cancelled (it did not fail), or when something was waiting for the task at the moment it failed (that wait gets the error). A `task-wait` made later on the finished task still gets the error; the hook was told once, when it happened. Keep the hook short and do not call `Shutdown` from it, which waits for it.
- **`Shutdown(ctx)` cancels every detached task and waits for them**, for as long as `ctx` allows, and returns `ctx`'s error if some are still running then (a handler that ignores its context can do that; the message says how many). It also stops the detached timers, closing their channels so nothing waits forever. It can be called more than once and from any goroutine. From its first call `spawn-detached` and `after-detached` are refused; evaluations otherwise go on. Without `Background` it does nothing.
- **A deadlock is the evaluation's, never a detached task's.** A detached task parked on a channel is waiting for the outside, which may be a later evaluation or the host, so it is neither reported as deadlocked nor killed when an evaluation's deadlock is. While one is running (not parked) an evaluation that waits for it is not deadlocked either, as for any task in a host call.

### Go handles for tasks and channels

`scheme.AsTask(v)` and `scheme.AsChannel(v)` return small interfaces for a value a script handed you, so a host does not have to apply Scheme primitives to look at them. Every method is safe from any goroutine, and none takes part in deadlock detection: a host is not a task, so using these never makes a script's wait look deadlocked, and never hides one that is.

```go
type Task interface {
    Value() Value                  // to hand back to a script
    Done() <-chan struct{}         // closed when it has finished
    Result() (Value, error)        // after Done; ErrTaskRunning before
    Cancel()
}

type Channel interface {
    Value() Value
    TrySend(v Value) (sent bool, err error) // never waits; an error means closed
    TryRecv() (v Value, ok, closed bool)    // ok: a value; closed: closed and drained; neither: nothing ready
    Recv(ctx context.Context) (v Value, ok bool, err error) // waits for the channel, and stops with ctx
    Close()                                 // idempotent
    Closed() bool
    SetHostFed(bool)
}

ch, err := scheme.NewChannel(in, 8, scheme.HostFed()) // the interpreter needs WithTasks
in.Global().Define("inbox", ch.Value())
```

`Result` of a task that was cancelled is an error for which `errors.Is(err, context.Canceled)` holds. A task that has ended is "done" for `Done` and `task-done?` alike; so is the task that started an evaluation, once the evaluation returns. `TrySend` on a channel with no buffer succeeds only if a receiver is parked, so give a channel the host sends to some capacity. A value passes through unchanged, `Taint` included: a channel does not launder provenance, whichever side sends.

### Host-fed channels and timers

A script that waits on a channel nobody inside the interpreter will send to, while nothing else is running, gets `deadlock: all tasks are waiting for one another and none can run`. When the sender is the host, that is a false alarm, and the channel says so: `scheme.NewChannel(in, n, scheme.HostFed())`, or `ch.SetHostFed(true)` on one a script made (it applies to tasks that wait afterwards). A task waiting on a host-fed channel, alone or in a `select`, is not counted as deadlocked, and does not make the others look deadlocked either: it can be woken from outside. It still stops with its context and with `task-cancel!`. The cost is that a wait on that channel nobody ever answers lasts until the context ends, so give such an evaluation a context with a deadline.

**Timers.** `(after secs)` belongs to the evaluation that made it: it is cancelled when the evaluation returns, so a channel from one evaluation never fires for a later one (this was measured on v0.5.0 and is the documented behaviour). `(after-detached secs)` is the interpreter's: it fires whoever holds the channel, after the evaluation is gone, and a task waiting on it is not a deadlock. After it has fired it is an ordinary channel, and `Shutdown` closes the ones still pending. It is what a detached task `select`s on for a timeout.

### Context values reach every task

**Guarantee.** A value the host puts in the context passed to `EvalString`, `Apply` or `Eval` is visible through `Call.Ctx` (and `ctx.Value`) to every primitive called, at any depth, beneath that evaluation: in the evaluation's own task, in `spawn`, `group-spawn`, `pmap`, `pmap-limit`, `pfor-each` and `race` tasks (workers included), in a `with-timeout` body, in callbacks that go through `Apply` (`map`, `sort`, `swap!`, `with-lock`), and in `spawn-detached` tasks and everything those start, even when the task outlives the evaluation and is waited for by one with different values. A detached task keeps the values of the evaluation that spawned it, not of the one that waits for it. Concurrent evaluations with different values never see each other's. `TestTheHostsContextValueIsVisibleInEveryKindOfTask` and `TestContextValuesDoNotCrossBetweenEvaluations` pin it.

What is *not* promised: a context you build inside a handler and pass to `Apply` instead of `Call.Ctx` starts a new evaluation and carries only what you put in it. Pass `Call.Ctx` on.

### Reading the interpreter from other goroutines

These two are confirmed by tests, and hold with `WithTasks` on:

- **A closure applied on one goroutine while another evaluation runs on the same `Interp`** is safe, including `Env.Define`, `Snapshot` and `Restore` on a module's environment (an `Env` you made with `scheme.NewEnv(in.Global())`) at the same time and detached tasks running. A host can therefore run one editor "transaction" at a time while detached tasks go on. Each operation is atomic; a transaction that wants all-or-nothing takes a `Snapshot` first and `Restore`s on failure, as always. `TestClosuresAndModuleEnvironmentsAreSafeWhileOtherEvaluationsRun`.
- **A `Gate` that implements `ConcurrentGate` is called without the interpreter's lock**: two calls can be inside it at the same moment, and one that does not implement it is never called twice at once. `TestAConcurrentGateIsCalledWithoutTheInterpretersLock`.

### Migration note: the Coigüe pattern

v0.5.0 hosts that wanted work after a command returned polled with `(channel-try-recv ch sentinel)` from a script, made channels with `(make-channel 1)`, and could not ask a task whether it was done without applying Scheme. From v0.6.0 the host owns the loop and turns every asynchronous thing into a callback that runs as a *new command*:

1. Grant `Background`, set `WithTaskFailed`, and call `Shutdown` when the editor closes.
2. Register primitives that hand a task or a channel to the loop together with a handler procedure: `(on-done task handler)`, `(on-message channel handler)`. They use `AsTask` and `AsChannel`.
3. The loop watches each with its own goroutine, `select`ing on `Task.Done()` or looping on `Channel.Recv(ctx)`, and **posts** a function to the UI goroutine. Nothing waits on Scheme there.
4. The UI goroutine runs posted functions one at a time; each is one short synchronous command (`Apply` the handler with the result). Failures arrive the same way, from the hook.
5. Channels the host sends to are `HostFed`; use `after-detached` for timers a detached task selects on.

```go
type Loop struct {
    in     *scheme.Interp
    events chan func()   // run on the UI goroutine, one at a time
    quit   chan struct{} // closed by Close
    wg     sync.WaitGroup
}

func (l *Loop) post(f func()) {
    select {
    case l.events <- f:
    case <-l.quit:
    }
}

// onDone is the Go side of (on-done task handler).
func (l *Loop) onDone(tk scheme.Task, handler scheme.Value) {
    l.wg.Add(1)
    go func() {
        defer l.wg.Done()
        select {
        case <-tk.Done():
            if v, err := tk.Result(); err == nil {
                l.post(func() { l.in.Apply(context.Background(), handler, []scheme.Value{v}) })
            } // else: WithTaskFailed has it, if nobody was waiting
        case <-l.quit:
        }
    }()
}

// onMessage is the Go side of (on-message channel handler).
func (l *Loop) onMessage(ch scheme.Channel, handler scheme.Value) {
    l.wg.Add(1)
    go func() {
        defer l.wg.Done()
        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()
        go func() { <-l.quit; cancel() }()
        for {
            v, ok, err := ch.Recv(ctx)
            if err != nil || !ok { // stopped, or the channel was closed
                return
            }
            l.post(func() { l.in.Apply(context.Background(), handler, []scheme.Value{v}) })
        }
    }()
}

// UI goroutine:  for f := range l.events { f() }
// On exit:       close(l.quit); in.Shutdown(ctx); l.wg.Wait()
```

`TestTheEventLoopPatternOfTheMigrationNote` runs this shape end to end. Two things to carry over from the old polling code: the handler is applied on a context of the host's choosing, so put the per-module value (the Gate reads it) in that context when you `Apply`; and the work itself, in the detached task, runs under the context values captured at `spawn-detached`.

### Different policies for different callers

`std.Install` enforces one `Policy` per interpreter inside the primitives. A host that runs several modules, each with its own grant (`(requires (net (allow "api.example.com") (methods get)))`), installs the widest policy any of them may have and narrows it to the calling module's `*std.Policy` in two places:

- **The network, with `std.WithNetCheck`.** A request is more than its arguments: a redirect leads to another URL, perhaps with another method, and a host name becomes an address only when the connection is made. The check judges each of those steps, with the request's context, which carries the evaluation's values (from tasks too), so it knows the caller.
- **The rest, in the `Gate`**: whether the caller has the module at all, and the command `proc-run` starts.

```go
rt, err := std.Install(in, widest, std.WithNetCheck(func(ctx context.Context, ev std.NetEvent) error {
    caller := policyOf(ctx) // from the value Coigüe put in the context
    if caller == nil {
        return nil // the host's own evaluations: the installed policy alone
    }
    if err := caller.CheckNetEvent(ev); err != nil {
        return &scheme.Denied{Prim: "http-request", Reason: err.Error()}
    }
    return nil
}))

func (g *myGate) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
    caller := policyOf(ctx)
    if m := g.rt.ModuleOf(p.Name); m != "" && !caller.Has(m) {
        return &scheme.Denied{Prim: p.Name, Reason: "this module has not been granted " + m}
    }
    if p.Name == "proc-run" {
        // CheckExec(argv[0])
    }
    return nil
}
```

The check is asked at three stages, each after the installed policy has allowed it, so it can only narrow:

| `ev.Stage` | When | Fields |
|---|---|---|
| `std.NetRequest` | before the request is sent | `Method`, `URL` |
| `std.NetRedirect` | before each redirect hop is followed | `Method`, `URL` of the hop. A 301, 302 or 303 turns a POST into a GET, and the hop is judged with the method it is sent with |
| `std.NetConnect` | immediately before each socket connects, on the dialer's hook that already guards the installed policy | `Host` (the name the URL gave), `Addr` (the address and port being connected to), `Private` |

`(*Policy).CheckNetEvent(ev)` is `CheckNet(method, url)` for the first two and `CheckAddr(host, ip)` for the third, which refuses a loopback, private, link-local, multicast, unspecified or reserved address unless the policy says `(private #t)`. The address is the one the socket is connecting to, not one looked up earlier, so a name whose answer changes between the request and the connection is judged by the answer used. An error refuses the step; it is what the script sees and what `WithAudit` records. Returning a `*scheme.Denied`, as above, makes the refusal one `guard` cannot catch, as the Gate's is; a plain error is catchable, like the installed policy's own refusals. Headers the script set are still dropped when a redirect changes host.

While a check is installed, a request's connections serve that request alone (no keep-alive, no shared HTTP/2 connection), since a connection the check allowed for one caller must not carry another's request. The function is called from several goroutines at once, the dialer's among them, so it must be safe for that, and should honour `ctx`. A host that passes no check behaves as before.

**Why not the Gate alone.** Up to v0.7, checking `http-request`'s arguments with `CheckNet` in the Gate was the pattern shown here. The Gate sees the arguments and nothing after them, so a redirect from a host the caller may reach went on to any host the *installed* policy allowed, and a name that resolves to a loopback address got through for a caller without `(private #t)` whenever the installed policy had it. A Gate that still checks `http-request` is harmless, but it is not enough: move the network check to `WithNetCheck`.

`CheckNet`, `CheckAddr` and `CheckExec` return an error written for a person ("net: evil.com:443 is not a host this module may reach (allowed hosts: api.example.com)") and make the same decision the installed procedures do. The installed policy stays the outer limit, so a check that is too loose cannot reach past it. `Runtime.ModuleOf(procedureName)` names the module a procedure came from. **There is no `CheckFS`**: the file system's containment is the operating system's (`os.Root`, which refuses a symbolic link out of the tree), and a check that only reads the path would let a link inside one module's tree lead into another's. For different file trees install different interpreters. `TestAPolicyPerCallerAllowsOneAndDeniesAnother` is the example, running against a real server; `std/netcheck_test.go` has the redirect, the loopback name and the method of a hop.

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
| A detached task outlives its evaluation and is awaited from the next | `TestADetachedTaskOutlivesItsEvaluationAndIsAwaitedFromTheNext` (`scheme/background_test.go`) |
| `Shutdown` cancels detached tasks, waits, and is bounded by its context | `TestShutdownCancelsEveryDetachedTaskAndWaits`, `TestShutdownIsBoundedByItsContext`, `TestShutdownRacesWithSpawning` |
| An unobserved detached failure goes to the host hook, not to an evaluation | `TestAnUnobservedDetachedFailureGoesToTheHostHook`, `TestAFailureSomeoneIsWaitingForIsTheWaitersAndNotTheHosts` |
| Go handles for channels and tasks, from several goroutines | `TestChannelFromManyGoroutines`, `TestChannelContract`, `TestTaskFromManyGoroutines` |
| A host-fed channel is not a deadlock; the host waiting does not hide one | `TestAHostFedChannelIsNotADeadlock`, `TestTheHostWaitingDoesNotHideAScriptsDeadlock` |
| A detached timer survives its evaluation | `TestADetachedTimerFiresForALaterEvaluation` |
| Context values reach every kind of task | `TestTheHostsContextValueIsVisibleInEveryKindOfTask`, `TestContextValuesDoNotCrossBetweenEvaluations` |
| Closures, module environments and `ConcurrentGate` under concurrency | `TestClosuresAndModuleEnvironmentsAreSafeWhileOtherEvaluationsRun`, `TestAConcurrentGateIsCalledWithoutTheInterpretersLock` |
| The event-loop pattern | `TestTheEventLoopPatternOfTheMigrationNote` |
| A policy per caller | `TestAPolicyPerCallerAllowsOneAndDeniesAnother`, `TestCheckNetAgreesWithTheInstalledPolicy`, `TestCheckExec` (`std/check_test.go`), `TestCheckAddr` |
| The caller's network check holds at every redirect hop and connection, and only narrows | `TestANetCheckRefusesARedirectOutsideTheCallersGrant`, `TestANetCheckJudgesTheAddressANameResolvesToForEachCaller`, `TestANetCheckJudgesTheMethodOfEachHop`, `TestANetCheckCannotWidenTheInstalledPolicy`, `TestANetCheckIsAskedAtEveryStepWithTheCallersContext` (`std/netcheck_test.go`) |
