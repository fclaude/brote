# Design: tasks, channels and shared memory

Status: implemented (see [What was built](#what-was-built) for where the code
differs from the proposal below). This is the design record, not user
documentation: the language reference is [scheme.md](../scheme.md#tasks) and the
embedding rules are in [embedding.md](../embedding.md#tasks). The examples on this
page are not run by the tests.

Brote gets **tasks** that behave like goroutines: `(spawn thunk)` starts one, it
runs **in parallel** with the others on all the cores the host allows, it shares
the program's data, and tasks talk through channels. The design has one rule
that everything else follows from:

> **No script can corrupt the interpreter or crash the host.** Every operation
> on a mutable value is atomic and tear-free, whatever other tasks are doing.
> Races between *sequences* of operations are the script's to avoid, and the
> language gives it tools that make them easy to avoid.

That is the memory-safe version of Go's model. Go lets a data race corrupt
memory; Brote is a sandbox for code the host does not trust, so it cannot.

An earlier draft of this design ran one task at a time and passed a baton at
blocking points. It is not what is proposed, for the reasons in
[Why not a baton](#why-not-a-baton).

## What the interpreter does today

1. **One goroutine owns an `Interp`.** `docs/embedding.md` says so, and the code
   is why: `Env` frames are plain `map[string]Value`, vectors are a slice, hash
   tables are a slice plus a map. Two goroutines that write the same one can
   end the process with `concurrent map writes`.
2. **Some state belongs to a running evaluation, not to the interpreter:**
   `Interp.out` and `Interp.printed` (swapped by `with-output-to-string`),
   `Interp.depth` (the `MaxDepth` counter) and `Interp.loading`.
3. **The evaluator is recursive Go,** and builtins such as `map`, `sort` and
   `hash-update!` call back into `Apply`. A task therefore needs a Go stack,
   which means a goroutine.
4. **Everything crosses one door,** `applyPrim`: arity, `expandArgs`,
   `Gate.Before`, handler, taint. `Origin` rides in the `context.Context`;
   `Denied`, `Halt` and a cancelled context are the errors `guard` cannot catch.
   Tasks must not open a second door or a way around any of this.

### The audit

Every piece of shared mutable state, and what happens to it.

| State | Today | In this design |
|---|---|---|
| `Env` frame (`vars` map) | unsynchronised | Per-frame `RWMutex`. The **global** frame, which every task reads on every call, is a `sync.Map` and takes no lock to read. |
| `Vector.Items` | unsynchronised | Per-vector `RWMutex`. |
| `Hash` (`entries`, `index`, `live`) | unsynchronised | Per-table `RWMutex`. Iteration (`hash-keys`, `each`) copies under the lock and walks the copy. |
| `Closure.Name`, set by `define` after the closure exists (`forms_binding.go:22`) | a write to a shared object | Set only on a closure no one else holds yet, or with an atomic store. |
| `Interp.depth` | one shared atomic counter | **Per task.** Measured below: the shared counter is what stops the interpreter scaling. |
| `Interp.out`, `Interp.printed` | swapped by `with-output-to-string` | **Per task** (a capture is the capturing task's). The base writer is wrapped in a mutex so concurrent `display` calls do not interleave mid-write. |
| `Interp.loading` | a map | A mutex. Two tasks loading the same file get the existing "loaded again" error. |
| `Interp.prims`, `Register`, `register-tool` | a map | A `RWMutex`. After `Freeze` the table is read-only and lock-free. |
| `Gate.Before` | called by one goroutine | Called under a mutex unless the gate says it is concurrent-safe (see [Embedding](#embedding)). |
| `Pair`, `string`, numbers, `Taint` | immutable | Nothing to do. |

Two rules keep the locks from ever deadlocking:

1. An object lock is held for the length of one primitive operation and **never
   across a call into Scheme, a gate or a handler.** Object locks are leaves, so
   there is no lock order to get wrong.
2. Walks over nested data (`equal?`, `DeepTaint`, the printer, `json-emit`) lock
   one object at a time, copy what they need and release before descending. They
   already carry a seen-set for cycles. A walk sees each object consistently,
   not the whole structure at one instant, which is the same promise Go's
   `sync.Map.Range` makes.

## The memory model

This is the part users read. It is short on purpose.

1. **Each primitive operation on a vector, hash table, variable, atom or channel
   is atomic.** `vector-set!`, `hash-set!`, `set!` and the rest never tear and
   never crash.
2. **A sequence of operations is not.** `(set! n (+ n 1))` read `n`, then wrote
   it; another task's write in between is lost. This is the same as Go.
3. **Synchronisation orders memory.** What a task did before one of the
   following is visible to the task that does the matching action after it:
   `spawn` → the new task's first step; `channel-send!` → the matching
   `channel-recv`; `channel-close!` → a receive that sees it closed; a task's
   last step → `task-wait` returning; `with-lock` release → the next acquire.
4. **The language provides the tools for the compound cases:**
   `hash-update!` and `swap!` apply a function atomically; `with-lock` guards a
   region; a channel hands data from one owner to the next.
5. **Prefer handing data over to sharing it.** The idiom is Go's: have one task
   own a structure and talk to it through a channel, rather than lock it.

The two things that surprise people in Go are fixed rather than documented:

- `hash-update!`, today a read, a callback and a write, becomes atomic. It reads
  the entry, calls the function **outside** the lock, and writes only if the
  entry is unchanged, else it retries. The function may therefore run more than
  once and should not have side effects, which `swap!` documents too. The common
  counter, `(hash-update! counts word (lambda (n) (+ n 1)) 0)`, becomes safe with
  no change to the script.
- A script cannot forget to unlock. There is no bare `lock`/`unlock`, only
  `with-lock`, which releases on error and on cancellation.

## The Scheme surface

No new special forms. `isSF` is the fixed list of reserved names; a form called
`go` would break every script that defines a function named `go`. Everything is
a procedure that takes a thunk where Go takes a function literal.

### Tasks

| Procedure | Meaning |
|---|---|
| `(spawn thunk)` | Start `thunk` as a new task and return its handle at once. |
| `(task-wait t)` | Block until `t` finishes; return its value or raise its error. |
| `(task-wait-all ts)` | `task-wait` on each, in order, returning the values. |
| `(task-done? t)` | Whether `t` has finished. Never blocks. |
| `(task-cancel! t)` | Cancel `t`'s context. It unwinds at its next step, and wakes from any wait, including an HTTP request or a program in flight. |
| `(current-task)`, `(task? x)` | |

### Structured concurrency, because users forget to wait

`spawn` alone leaves it to the script to wait for everything and to stop siblings
when one fails. The common cases are built in, so scripts rarely write that code:

| Procedure | Meaning |
|---|---|
| `(task-group f)` | Call `(f group)`. Tasks started with `(group-spawn group thunk)` are all waited for before `task-group` returns. The first to fail cancels the rest, and its error is the group's (like Go's `errgroup`). Returns `f`'s value. |
| `(pmap f xs)` | `map`, with the calls running in parallel. Results are in order. First error cancels the rest. |
| `(pmap-limit n f xs)` | The same, with at most `n` at a time: the form to use for a thousand URLs. |
| `(pfor-each f xs)` | |
| `(race thunks)` | Run the thunks in parallel; return the first value; cancel the others. |
| `(with-timeout secs thunk)` | Run `thunk` with a deadline. At the deadline the call and everything it started is cancelled, **including in-flight host calls** (`http-request` and `proc-run` both honour `Call.Ctx` already), and the caller gets an ordinary "timed out" error. |

```scheme
(define (fetch url) (http-request "GET" url))

(pmap-limit 8 fetch urls)                         ; at most 8 in flight

(with-timeout 5 (lambda () (fetch "https://example.com/slow")))

(define counts (make-hash))
(pfor-each (lambda (w) (hash-update! counts w (lambda (n) (+ n 1)) 0))
           words)                                  ; safe: hash-update! is atomic
```

### Channels

| Procedure | Meaning |
|---|---|
| `(make-channel)`, `(make-channel n)` | Unbuffered (a rendezvous), or buffered with room for `n`. |
| `(channel-send! ch v)` | Block until delivered or buffered. Raises on a closed channel. |
| `(channel-recv ch)`, `(channel-recv ch eof)` | Block for a value. On a closed, drained channel: raise, or return `eof` when given (Brote has no multiple values to carry Go's `ok`). |
| `(channel-try-recv ch fallback)` | Never blocks. |
| `(channel-close! ch)`, `(channel-closed? ch)` | Closing wakes every waiting receiver. |
| `(channel-for-each f ch)` | `f` on each value until the channel is closed. |
| `(select cases)`, `(select cases default-thunk)` | Wait on several channels. A case is `(recv ch)` or `(send ch v)`. Returns `(index . value)`. **When several are ready, one is chosen at random**, as in Go, so no case starves. |
| `(after secs)` | A channel that receives once after `secs`, for use in `select`. |

### Locks, atoms and once

| Procedure | Meaning |
|---|---|
| `(make-mutex)`, `(with-lock m thunk)` | Mutual exclusion. Not reentrant: locking twice from one task is a deadlock, and the deadlock detector says so. |
| `(make-atom v)`, `(atom-ref a)`, `(atom-set! a v)` | A shared cell with atomic reads and writes. |
| `(swap! a f)` | Atomically replace the value with `(f value)`: read, call `f` outside any lock, install if unchanged, else retry. `f` may run more than once, so keep it pure. |
| `(compare-and-set! a old new)` | |
| `(make-once)`, `(once-do o thunk)` | Run `thunk` exactly once however many tasks ask. |

A script that wants a shared counter writes `(swap! n (lambda (x) (+ x 1)))`, and
that is the whole of what it has to know.

## Errors, cancellation and lifetime

These follow `guard`'s existing rules instead of adding new ones.

- A task that raises an ordinary error finishes *failed*; `task-wait` re-raises
  it in the waiter, whose `guard` may catch it. The error keeps its `Taint`.
- A cancelled task unwinds as `Catchable` already defines: **its own `guard`
  cannot catch it.** `task-wait` on a cancelled task raises an ordinary "task
  cancelled" error in the waiter, which can catch it because the waiter was not
  cancelled.
- **A `Denied` or `Halt` in any task ends the whole evaluation:** every other task
  is cancelled and the root `EvalString`/`Apply` returns that error. `task-wait`
  does not make it catchable. A script cannot hide a refusal by running the
  refused call in a task and ignoring the task.
- **Tasks belong to the evaluation that started them.** When the root
  `EvalString`, `Apply` or `LoadFile` returns, every task still running is
  cancelled and waited for before it does. Nothing leaks and nothing runs between
  calls. This is stricter than Go and the same as structured concurrency. If the
  root succeeded but a task failed with an error no one waited for, the root
  returns that error: a silent background failure is what an orchestrator least
  wants. A script that wants fire-and-forget `guard`s inside the thunk.
- **Deadlock** is detected. The scheduler counts tasks that are running (a task
  inside a host call counts as running) against tasks blocked on a channel, lock
  or `task-wait`. When none are running, no timer is pending and some are blocked,
  the root task gets a `deadlock: all tasks are blocked` error and the rest are
  cancelled, instead of a hang until the context's deadline. Go treats this as
  fatal; here it is an error the host can show.
- `WithMaxTasks(n)` (default 256) bounds live tasks, and `spawn` past it is an
  error. A task's goroutine and stack are the cost, and the `MaxDepth` comment
  puts a deep `Eval` at one to five kilobytes a frame. Depth is per task, with a
  separate, smaller `WithTaskDepth` for hosts that run untrusted code.

## Embedding

Hosts must be able to adopt this without auditing every handler they wrote for a
single-goroutine world, so tasks are a **grant**, like a `std` module:

```go
in := scheme.New(scheme.WithTasks(scheme.TaskLimits{MaxTasks: 64, MaxDepth: 2_000}))
```

Without `WithTasks`, `spawn` and the rest are not defined, and nothing changes:
the interpreter is the single-goroutine one in `embedding.md`. The `brote`
command turns tasks on. `WithTasks` is the host's statement that its handlers
are safe to call from several goroutines.

- **Handlers.** `std`'s primitives are audited and made safe as part of this work
  (the audit list is below). A host with a handler that is not goroutine-safe
  wraps it: `Handler: scheme.Serialize(h)` runs it under its own mutex. It must
  not be used on a handler that calls back into Scheme, which could then wait for
  itself, and the doc says so.
- **The gate.** `Gate.Before` runs under an interpreter-wide mutex by default, so
  every existing gate keeps working unchanged, including one that stops to ask the
  person (one question at a time, and the answers are totally ordered in the
  audit log). A gate that is safe to call concurrently says so by implementing
  `scheme.ConcurrentGate`, and is then called without the mutex.
- **Context and origin.** `spawn` derives the task's context from its spawner's
  (`context.WithCancel(ctx)`), so a task started by a model-originated call is
  model-originated and cannot be more trusted than its parent. `Call.Ctx` is the
  task's context, so a handler that honours it is cancelled with its task.
- **The gate sees tasks.** `spawn` is a primitive, so it crosses the gate: a host
  policy can refuse it, and `TestGateIsTheSingleExit` keeps holding.
- **Taint.** Values carry their `Taint` through channels, atoms, `task-wait` and
  a task's result. A channel does not launder provenance. A test pins it.
- **Concurrent callers.** Because the heap is now safe, a host with `WithTasks`
  may call `EvalString` or `Apply` from several goroutines at once; each call is
  its own root evaluation with its own tasks. `Register`, `Freeze`,
  `BeginReload` and `EndReload` stay single-threaded, between evaluations.
- **Long-lived tasks.** Draining at the end of a root forbids a task that outlives
  its command, which an editor host such as Coigüe may want. A second grant,
  `TaskLimits.Background`, adds `(spawn-detached thunk)`, owned by the
  interpreter and cancelled by `in.Shutdown(ctx)`.

## Implementation

### An explicit `*task` in the evaluator

Per-task depth, output and captured taint have to be reached on every `Eval`
without a `ctx.Value` walk in the hottest function. `Eval(ctx, x, env)` stays as
the exported entry point and finds the task in `ctx` once. It calls an internal
`eval(t *task, ctx, x, env)` that threads the task through every recursive call,
and `Call` carries it into handlers, so `display` and `with-output-to-string`
write to the calling task's output. About forty call sites in the `scheme`
package change, all mechanically.

### Frames

A `Define` on a local frame can race with a `Lookup` from a task that captured it
(`(define (f) (g))`, `(spawn f)`, then `(define (g) …)` in the same body), so a
frame that could be shared is locked. The cost was measured (next section) and is
not visible. The representation is free to improve on a map per frame: a small
slice of bindings with a linear scan is one allocation instead of several, and
does not change the locking.

### Channels, select, joins and locks on one wait-queue core

Everything a task waits for (a channel, a `select`, `task-wait`, a lock, a group,
a timer) parks on one core, under one mutex per interpreter (`sync.go`). The
proposal wrapped Go channels and `reflect.Select`; that cannot say *exactly*
when every task is waiting, because a task is "about to wait" before its
channel operation actually parks, and counting it then reports a deadlock that
a sender one instruction away would have prevented. With one lock, "this task
parks" and "that task wakes it" are decided together, so detection is exact and
needs no timeout.

## What was measured

Throwaway prototype in a scratch copy of the repository (not in this tree): `Env`
with a `RWMutex` per local frame and a `sync.Map` for the global frame, and the
shared depth counter removed. Benchmark: `(fib 22)` in one interpreter, one
goroutine, then 16 goroutines each running it, on a 16-core machine. The 16
goroutines do 16 times the work, so 16 × the serial time is the serial
equivalent.

| Configuration | Serial | 16 goroutines | vs. serial equivalent (≈1.1 s) |
|---|---|---|---|
| Today (no locks; works only because `fib` never writes a shared map) | 69 ms | 1,460 ms | 0.75× (slower) |
| Locked frames, `sync.Map` global, shared depth counter | 70 ms | 1,320 ms | 0.83× |
| Locked frames, `sync.Map` global, **no shared depth counter** | 68 ms | 700 ms | 1.6× |
| The same, `GOGC=800` | 66 ms | 343 ms | 3.2× |

What it says:

1. **The locks are not the problem.** Serial time is unchanged within noise
   (68 to 74 ms against 69 ms), so there is no need for escape analysis, a
   "no tasks yet" fast path, or any other trick that would risk soundness to save
   them.
2. **The shared `depth` counter is.** One atomic add on a cache line that 16 cores
   fight over, on every `Eval`. It takes a 16-way run from slower than serial
   to faster. Per-task depth is a requirement, not a polish.
3. **After that the limit is allocation.** `(fib 22)` makes 1.03 million
   allocations and 221 MB, about 18 allocations and 3.9 KB per call. Raising
   `GOGC` fourfold doubles the speed-up, which is what a collector-bound program
   does. The interpreter spends its time making garbage.

So parallel CPU-bound speed-up is real but bounded today at about 1.6× on 16
cores, and **the work that raises it also speeds up every single-threaded script:**
frames as small slices instead of maps, `Slice()` that counts before it appends,
fewer argument slices. Profile first (`growslice`, `makemap` and `mallocgc` are
the top of the allocation profile). Targets, to be confirmed by the same
benchmark rather than promised: serial no slower than today at any step, and at
least 4× on 16 goroutines by the end of the allocation work.

The prototype did not touch vectors, hash tables or channels, and did not run the
race detector, which the real work does from the first commit.

## Static help

The repository already has a static analyser and a language server
(`analysis/`, `lsp/`). The mistakes this model permits are mostly syntactic, so
they get diagnostics, which is the other half of making it easy:

- `set!` on a variable defined outside a `spawn`, `pmap` or `group-spawn` thunk:
  "other tasks may write this at the same time; use `make-atom` and `swap!`, or
  `with-lock`."
- `(set! x (… x …))` in such a thunk, the read-modify-write form specifically.
- `channel-send!` or `channel-recv` inside `with-lock`, the usual lock-ordering
  deadlock.
- A `swap!` or `hash-update!` function that calls a primitive with side effects
  (`display`, anything `Host`), since it may run twice.

## Why not a baton

The first draft ran Scheme in one task at a time and switched only at channel
operations and blocking host calls. It was simpler to build and was the wrong
trade for the people using it:

- **No CPU parallelism,** and the answer to "I need it" was a second feature
  (isolated interpreters that copy their messages), which is the harder feature
  for a script author to use.
- **It needed an opt-in flag on every primitive that waits** (`Meta.Blocking`),
  plus a rule about which arguments were allowed, or a host's tool silently
  serialised everything. A script author could not tell why their HTTP calls did
  not overlap.
- **Atomicity between yield points looks like a feature and is a trap.** Scripts
  would come to rely on it, and it breaks the day a primitive gains a yield
  point, or a host turns preemption on.
- **It still needed per-task state and a task-aware evaluator,** so it saved
  little of the hard work.

What it got right is kept: tasks are goroutines, the gate is a single door, tasks
belong to their root evaluation, deadlock is an error, cancellation reuses
`Catchable`.

Not chosen either: **share-nothing tasks** (a thunk closes over its `Env`, so
copying the environment changes the meaning of `set!`), and **CPS or a register
machine** (right in the long run, and every builtin that calls `Apply` would be
rewritten with it).

## What was built

The proposal above was built in seven commits (`git log --oneline -- docs/design/tasks.md`).
Where it differs:

- **Cases are `(on-recv ch)` and `(on-send ch v)`**, not `(recv ch)` and `(send ch v)`,
  so a list of cases needs no quoting. `select` with a default returns
  `(-1 . result)` of the default procedure.
- **The wait-queue core** above, instead of Go channels.
- **`pmap` and `pfor-each` run a pool** of as many workers as the task limit
  leaves room for, which share the items, so a list of a thousand needs no
  `pmap-limit` to stay under `MaxTasks`.
- **`with-timeout` runs the thunk in the calling task** under a deadline that is
  one of the interpreter's timers, rather than in a new task. A guard inside it
  cannot catch the timeout, a request in flight is cancelled, and a wait it will
  end is not reported as a deadlock.
- **Native objects** (tasks, channels, locks, atoms) are a new value kind,
  `KObject`, held in the existing `Handle` field so `Value` did not grow.
- **The tasks of an evaluation end with it, and the error says so** when a later
  evaluation waits for one. The REPL treats each line as an evaluation, so tasks
  and their waiting go in one expression there. A `Background` grant for
  tasks that outlive a call (open question 2) is not built.
- **Diagnostics** are in `analysis/concurrency.go`: shared `set!` from a task,
  waiting while holding a lock, and effects in a `swap!` or `hash-update!`
  procedure.
- **`std`'s audit** found three real problems, all fixed: the regexp cache, the
  lazily opened directory roots, and the audit callback.

### Results

`(fib 22)` in one interpreter on a 16-core machine; the 16-goroutine run does
16 times the work.

| | Before | After |
|---|---|---|
| Serial | 73 ms | 51 ms |
| Allocations per run | 1.03 M | 0.40 M |
| Bytes per run | 223 MB | 59 MB |
| 16 goroutines | 1.34 s | 0.28 s |
| Speed-up over running them serially | 0.9× | 2.9× |
| Definitions, loops and `map` (`BenchmarkBodies`) | 24.5 ms | 15.6 ms |

The 4× target for 16 goroutines is not met: collection and memory bandwidth
still limit it. What is left is `Value` itself (about 112 bytes, copied
everywhere), the argument slice each application makes, and the `Call` each
primitive call allocates.

## Order of work

1. **Make the heap safe, before any task exists.** Locked frames and a
   `sync.Map` global, locked vectors and tables, atomic `hash-update!`,
   `Closure.Name`, the prims and loading locks, the locked base writer. Add the
   `*task` plumbing and per-task depth, output and captured taint. This step
   changes no behaviour and is tested by hammering shared structures from plain
   Go goroutines under `-race`.
2. **The scheduler:** `spawn`, `task-wait`, `task-cancel!`, the error rules
   (`Denied` and `Halt` propagate; cancellation uncatchable inside, catchable
   outside), draining, limits, `WithTasks`, the gate mutex.
3. **Channels, `select`, deadlock detection.**
4. **`task-group`, `pmap`, `race`, `with-timeout`, atoms, mutex, once.**
5. **Audit and fix `std`:** `fs` (the `os.Root` handles and any cache in `fsys`),
   `net`, `exec`, `plugin`, `Runtime.closers`. Add `scheme.Serialize` and
   `ConcurrentGate`.
6. **Allocation work,** measured against the benchmark above.
7. **Diagnostics in `analysis/`,** documentation (`scheme.md`, `stdlib.md`,
   `embedding.md`; `TestDocCoverage` forces every new builtin onto the reference
   page), and the `brote` command enabling tasks.

## Tests

- Under `-race`: N tasks hammering one hash table, one vector, one captured
  variable, one atom, one channel. The race detector is the main oracle.
- `pmap-limit` over `time-sleep`: wall time is near the longest chain, not the sum.
- `hash-update!` and `swap!` from 16 tasks lose no update.
- Denial inside a task, with `guard` around `task-wait` in the root: the evaluation
  still ends with the denial.
- `task-cancel!` on a spinning task, on one blocked in `channel-recv`, on one
  inside `http-request`; and the host's context cancelling all of them.
- `with-timeout` cancels an in-flight host call.
- Taint survives a channel, an atom and `task-wait`.
- `with-output-to-string` around a body that blocks does not capture a sibling's
  output.
- Deadlock reports an error and leaves the interpreter usable; so does a lock
  taken twice.
- After the root returns, no goroutine from the call remains.
- Two goroutines calling `EvalString` on one interpreter, under `-race`.
- A script with no `spawn` runs unchanged; the conformance suite passes untouched;
  the benchmark above does not regress.

## Open questions

(Resolved ones are noted. 2 and 5 remain open.)

1. **Unobserved task errors** fail the root. The alternative is a host hook and a
   silent default. Loud is the proposal.
2. **Reentrant `with-lock`.** Not reentrant, so a function that takes the lock and
   calls another that does deadlocks, and the detector names it. Making it
   reentrant hides design errors. Proposal: not reentrant.
3. **`MaxDepth` per task.** 2,000 is a guess. Measure an `Eval` frame inside a task
   before fixing the default.
4. **`WithTasks` default for the library.** Off, so no embedder is surprised. If
   the `brote` command and `std` are the main users, it may be right to flip it
   before 1.0, when the API is still free to change.
5. **Isolated interpreters,** for a script that must not share memory with its
   workers at all (running code from two sources). With a safe shared heap this
   is no longer how scripts get parallelism, only a way to get isolation, and is
   a separate design.
