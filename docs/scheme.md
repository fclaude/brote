# Brote Scheme: language reference

Brote is a small Scheme for embedding in a Go program. A program written in it
decides *what to do*; every effect (reading a file, running a command, calling
a service) is a Go **primitive** that the host registers, and every primitive
application passes through one optional **gate** the host installs. Scheme
code never touches the network or the filesystem on its own; the one builtin
that reads a file, `load`, is a primitive like the rest and the gate sees it.

This document is the contract for the language. The interpreter implements
what is written here and nothing else; the conformance suite has a snippet for
every form and builtin below, and a builtin that is not documented here fails
the suite. How a Go program embeds the interpreter is in
[embedding.md](embedding.md); the standalone `brote` command is in the
[README](../README.md).

§9 lists what is deliberately absent.

---

## 1. Lexical syntax

| Token | Notes |
|---|---|
| `;` … end of line | comment |
| `#|` … `|#` | block comment; may span lines; `|#` ends it (nesting is not supported). Unterminated is *incomplete* input |
| `(` `)` | list delimiters; `[` `]` are **not** accepted |
| `#(` `)` | a vector: `#(1 2 "x")` |
| `'x` `` `x `` `,x` `,@x` | `(quote x)` `(quasiquote x)` `(unquote x)` `(unquote-splicing x)` |
| `"…"` | string; escapes `\"` `\\` `\n` `\t` `\r`, `\xHH` (one byte), `\uHHHH` and `\UHHHHHHHH` (one code point); may span lines |
| `"""…"""` | raw string: no escapes, so the text is exactly what the file holds. See §1.1 |
| `42` `-7` `+3` | integer (int64) |
| `3.14` `1e3` `-0.5` `.5` | float (float64) |
| `#t` `#f` | booleans |
| `.` | dotted pair inside a list: `(a . b)` |
| anything else | a symbol; case-sensitive; `+`, `-`, `...`, `1+` are symbols |

A token is parsed as an integer if `strconv.ParseInt` accepts it in base 10,
else as a float if `strconv.ParseFloat` accepts it and it is made only of
digits, signs, a point and an exponent, else it is a symbol: `inf`, `nan`
and `1_000` are symbols, and an integer too large for 64 bits reads as a
float.

Unbalanced input is reported as *incomplete*, which the REPL uses to keep
reading lines.

### 1.1 Raw strings

`"""` begins a raw string and the next `"""` ends it. Nothing between them
is an escape, so quotes and backslashes are themselves:

```lisp
(define pattern """\d+\.\d+""")
```

When the opening delimiter is followed immediately by a newline the block is
read as **indented text**: that first newline is dropped, the indentation
shared by every non-blank line and by the closing delimiter's line is
removed, and the newline before the closing delimiter is dropped. A block
therefore reads at the indentation it is written at and produces text with
none:

```lisp
(define instructions
  """
  Prefer table-driven tests.
  Run `make accept` before saying a change is done.
  """)
```

is exactly `"Prefer table-driven tests.\nRun `make accept` before saying a
change is done."`. Lines indented further than the margin keep the
difference, and blank lines stay blank.

Anything else is verbatim: `"""a "b" c"""` is `a "b" c`.

Note that `"""` always starts a raw string, so an empty string written
immediately before another string (`"""x"`) is read as a raw string rather
than as `""` followed by `"x"`. Separate them, as ordinary formatting
already does.

## 2. Data types

| Type | Written as | Predicate | Notes |
|---|---|---|---|
| empty list | `()` | `null?` | must be quoted in code: `'()` |
| boolean | `#t` `#f` | `boolean?` | only `#f` is false; everything else is true |
| integer | `42` | `integer?` | int64; wraps on overflow like Go |
| float | `2.5` `1.0` | `float?` | float64; always printed with a `.` or exponent |
| string | `"hi"` | `string?` | immutable UTF-8; lengths and indices count **runes** |
| symbol | `foo` | `symbol?` | interned by name |
| pair / list | `(1 2 3)` `(a . b)` | `pair?` `list?` | lists are chains of pairs ending in `()`; no mutation |
| vector | `#(1 2 "x")` | `vector?` | indexed array; `vector-set!` mutates; no mutation of lists |
| hash table | `#<hash ("a" . 1) (b . 2)>` | `hash?` | a mutable map from strings, symbols, integers and booleans, listed in the order the keys were added. It prints this way but has no written form to read back |
| procedure | `#<procedure f>` `#<primitive car>` | `procedure?` | closures and primitives are both procedures |
| handle | `#<cred:class:id>` | `handle?` | opaque value a host primitive hands out: a program can pass it around and compare it, never open it. No builtin converts it to a string, and everything derived from it carries its class as a provenance tag (§5) |
| void | `#<void>` | none | result of `define`, `set!`, `display`, …; the REPL does not print it |

`number?` is true for integers and floats.

## 3. Evaluation

- A symbol evaluates to its binding; an unbound symbol is an error.
- Numbers, strings, booleans and void are self-evaluating.
- `()` unquoted is an error ("empty application").
- A list whose head is one of the **special form** names below is that form.
  These names are reserved: binding one as a variable does not change how the
  form is parsed (this includes `with-output-to-string`).
- Any other list is an **application**: the head and every argument are
  evaluated left to right, then the procedure is applied.
- **Proper tail calls.** The last expression of a `lambda` body, `begin`,
  `let`/`let*` body, `cond` clause, `and`/`or`, and either branch of `if` is
  in tail position. A loop written as a tail-recursive function runs in
  constant stack space.
- **Nesting is bounded.** Evaluations nested inside each other (calls not in
  tail position, recursion through a primitive such as `map`) are limited to
  10,000; past that the evaluation raises `eval: recursion too deep: more than
  10000 nested evaluations`, an ordinary error `guard` can catch. The reader
  refuses a datum nested deeper than that, `json-emit` refuses a value nested
  deeper, `equal?` treats values nested deeper as equal only when they are the
  same object, and the printer writes `...` past that depth, so a vector that
  holds itself (`vector-set!`) prints, compares and emits without end. `load`
  refuses a file that is already being loaded. Without these bounds a runaway
  recursion overflowed Go's stack, which ends the whole process.

### Special forms

| Form | Meaning |
|---|---|
| `(define name expr)` | bind `name` in the current environment |
| `(define (name param … [. rest]) body …)` | shorthand for a named `lambda` |
| `(lambda (param … [. rest]) body …)` `(lambda args body …)` | closure; `rest`/`args` receive a list |
| `(if test then [else])` | missing `else` yields void |
| `(let ((v e) …) body …)` | bind in parallel |
| `(let name ((v e) …) body …)` | named let: `name` is bound to the loop procedure inside the body |
| `(let* ((v e) …) body …)` | bind sequentially |
| `(letrec ((v e) …) body …)` | mutual bindings: every initializer may reference the other names (a closure resolves them at call time) |
| `(case key [(datum …) body …] … [(else body …)])` | first clause whose datum list contains a member `equal?` to the key wins; no clause → void |
| `(do ((v init [step]) …) (test [e …]) cmd …)` | loop: check `test`; on false evaluate the `cmd`s, then the `step`s (in parallel), rebind, repeat. Value is the test clause's last `e` (void if none). A Go loop, so constant stack |
| `(when test e …)` | `e …` only if `test` is true; yields void otherwise |
| `(unless test e …)` | `e …` only if `test` is false; yields void otherwise |
| `(guard (var clause …) body …)` | evaluate `body …`; if it raises a Scheme error, bind `var` to the condition (an alist `((message . "text") (irritants …))`) and try the clauses like `cond` (`(test body …)`, `(test => proc)`, `(else …)`). The first true clause's value is the value of `guard`; no match re-raises. A guard **denial** and a **cancellation** are never catchable |
| `(with-output-to-string e …)` | evaluate `e …` with `display`/`write`/`newline` captured; return the captured text (a minimal string port) |
| `(begin e …)` | evaluate in order, yield the last |
| `(set! name expr)` | assign an **existing** binding; error if unbound |
| `(quote d)` | the datum itself |
| `(quasiquote d)` | like `quote`, but `(unquote e)` evaluates `e` and `(unquote-splicing e)` splices a list. One level only: nested quasiquote is not supported |
| `(cond (test body …) … [(test => proc)] [(else body …)])` | first true clause wins; `=>` passes the test value to `proc`; no clause → void |
| `(and e …)` | short-circuit; `(and)` is `#t` |
| `(or e …)` | short-circuit; `(or)` is `#f` |

`define` inside a `lambda` body binds in that call's environment (internal
definitions). When `define` names a fresh anonymous closure, the closure takes
that name for printing and error messages.

## 4. Errors

`(error message irritant …)` raises an error whose text is `message` followed
by the written irritants. Runtime failures (unbound variable, wrong argument
count, wrong type, division by zero, a denied primitive) raise the same kind
of error with a message of the form `who: what`.

Errors may be caught with `guard` (§3):

```lisp
(guard (e ((string-contains? (assoc-ref e 'message) "boom") 'caught)
          (else (assoc-ref e 'irritants)))
  (error "boom" 1 2))            ; caught
```

An error not caught by any clause unwinds to the host, which reports it (the
REPL prints it; `brote FILE` prints it and exits 1).

Three things pass through `guard` untouched, so that a program cannot hide
that it was stopped: a primitive the gate **denies** (it raises
`<name>: denied: <reason>`, a `*scheme.Denied` in Go), a **cancellation** (the
evaluation's context is done), and a **halt** (a host primitive returned a
`*scheme.Halt` to say the work is over). A primitive's own timeout while the
evaluation goes on is an ordinary error that `guard` catches.

## 5. Builtins

Signatures use `…` for variadic and `[x]` for optional. Every builtin is a
primitive in the table (§7) and can be listed with `(primitives)`.

### Numbers

| | |
|---|---|
| `(+ n …)` `(* n …)` | sum / product; `(+)` is `0`, `(*)` is `1` |
| `(- n m …)` | subtraction; `(- n)` negates |
| `(/ n m …)` | division; `(/ n)` inverts. Two integers divide exactly when the result is integral, otherwise yield a float. Division by zero is an error |
| `(= a b …)` `(< a b …)` `(> a b …)` `(<= a b …)` `(>= a b …)` | numeric comparison chains; mixed int/float compare by value |
| `(quotient n m)` `(remainder n m)` `(modulo n m)` | integer division family; integers only. `remainder` takes the sign of `n`, `modulo` the sign of `m` |
| `(abs n)` `(min n …)` `(max n …)` | |
| `(floor x)` `(ceiling x)` `(round x)` `(truncate x)` | integers pass through; floats stay floats (`(floor 2.5)` → `2.0`). `round` rounds half to even |
| `(exact x)` `(inexact x)` | float→int (truncates toward zero) / int→float |
| `(expt b e)` `(sqrt x)` | `expt` of two integers with `e ≥ 0` is an integer (an error when it does not fit 64 bits, unlike `+` and `*`, which wrap); everything else is a float |
| `(number? x)` `(integer? x)` `(float? x)` | |
| `(zero? n)` `(positive? n)` `(negative? n)` `(even? n)` `(odd? n)` | `even?`/`odd?` need integers |
| `(number->string n)` `(string->number s)` | `string->number` returns `#f` when `s` is not a number |

### Booleans and equivalence

| | |
|---|---|
| `(not x)` | `#t` only for `#f` |
| `(boolean? x)` | |
| `(eq? a b)` `(eqv? a b)` | **identity** for pairs, vectors, procedures and handles; **value** for everything else, including strings. `eqv?` is an alias |
| `(equal? a b)` | structural equality |

### Pairs and lists

| | |
|---|---|
| `(cons a d)` `(car p)` `(cdr p)` | |
| `(caar p)` `(cadr p)` `(cdar p)` `(cddr p)` `(caddr p)` `(cdddr p)` | compositions |
| `(list x …)` `(list? x)` `(pair? x)` `(null? x)` | `list?` is true only for proper lists |
| `(length l)` `(append l …)` `(reverse l)` | |
| `(list-ref l i)` `(list-tail l i)` | 0-based; out of range is an error |
| `(iota n)` | `(0 1 … n-1)`; `n` is at most 1,000,000 (more is an error: the list is built without a chance to abort) |
| `(memq x l)` `(member x l)` | tail starting at the first match (`eq?` / `equal?`), else `#f` |
| `(assq k al)` `(assoc k al)` | first pair whose car matches (`eq?` / `equal?`), else `#f` |
| `(map f l …)` | n-ary; stops at the shortest list |
| `(for-each f l …)` | for effect; yields void |
| `(filter pred l)` | |
| `(fold f init l)` | left fold: `(f acc x)` |
| `(sort l less?)` | stable; `less?` is a Scheme or primitive procedure |
| `(apply f arg … l)` | last argument must be a list |

### Vectors

| | |
|---|---|
| `(vector x …)` `(vector? x)` | build / predicate |
| `(vector-length v)` | |
| `(vector-ref v i)` | 0-based; out of range is an error |
| `(vector-set! v i x)` | mutate element `i`; yields void |
| `(vector->list v)` `(list->vector l)` | convert between vectors and lists |

`equal?` compares vectors elementwise; `eq?`/`eqv?` compare vectors by
identity.

### Hash tables

| | |
|---|---|
| `(make-hash)` `(hash? x)` | a new empty table / predicate |
| `(hash-set! h key v)` | store `v` under `key`, replacing what was there; yields void. A key already in the table keeps its place in the order |
| `(hash-ref h key [default])` | the value under `key`. When `key` is missing, `default` if one was given (it's the value itself, so a procedure is not called), otherwise an error that names the key |
| `(hash-has? h key)` | true if the table has an entry for `key` |
| `(hash-remove! h key)` | remove the entry; a missing key is not an error. Adding the key again puts it at the end |
| `(hash-count h)` | number of entries |
| `(hash-keys h)` `(hash-values h)` `(hash->alist h)` | the keys, the values, or `(key . value)` pairs, as new lists in the order the keys were added |
| `(alist->hash al)` | a table from an association list. A key that appears twice keeps its first place and takes the later value |
| `(hash-update! h key proc [default])` | store `(proc old)` under `key`, where `old` is the current value, or `default` when the key is missing (an error naming the key when there is no default). If `proc` raises an error nothing is stored |

A key is a string, a symbol, an integer or a boolean, and two keys are the
same when `equal?` says so: the string `"a"` and the symbol `a` are different
keys, and so are `1` and `#t`. Any other key is an error that names its type.
Values can be anything, including tables, and a table can hold itself.

Tables are shared the way vectors are: `(define g h)` makes a second name for
one table. `equal?` and `eq?` on tables are identity, so two empty tables are
not `equal?`. `write` and `display` print `#<hash (key . value) …>`; a table
met again inside itself prints as `#<hash ...>`. `json-emit` writes a table as
an object when every key is a string, and is an error otherwise (§5 JSON).
`json-parse` still gives an association list, and `alist->hash` turns that into
a table.

### Strings

| | |
|---|---|
| `(string? x)` `(string-length s)` | length in runes |
| `(string-append s …)` | |
| `(substring s start [end])` | rune indices; `end` defaults to the length; out of range is an error |
| `(string=? a b …)` `(string<? a b …)` | |
| `(string-upcase s)` `(string-downcase s)` `(string-trim s)` | `string-trim` strips Unicode whitespace at both ends |
| `(string-split s sep)` | `sep` is a literal separator (no regular expressions); empty `sep` is an error |
| `(string-join l sep)` | |
| `(string-contains? s sub)` `(string-prefix? prefix s)` `(string-suffix? suffix s)` | |
| `(string-replace s old new)` | replace every occurrence |
| `(string->symbol s)` `(symbol->string sym)` `(symbol? x)` | |
| `(format fmt arg …)` | `~a` display, `~s` write, `~%` newline, `~~` tilde; too few or too many args is an error |

There is no character type; one-character strings serve that purpose.

### Output

| | |
|---|---|
| `(display x)` | human form: strings without quotes |
| `(write x)` | machine form: strings quoted and escaped, readable back |
| `(newline)` | |
| `(with-output-to-string e …)` | a special form (reserved like the others): capture `display`/`write`/`newline` from `e …` and return the text |

Output goes to the interpreter's output writer, which the host provides
(the REPL uses stdout; an embedding host captures it or discards it). All three yield void.

### Control

| | |
|---|---|
| `(procedure? x)` | |
| `(apply f arg … l)` | see lists |
| `(void)` | the void value |
| `(error message irritant …)` | raise; see §4 |

### Tasks

Available when the host gives the interpreter tasks (`scheme.WithTasks`; the `brote` command does). A task is a piece of Scheme running at the same time as the rest of the program, on its own core if there is one, sharing the program's variables, vectors and hash tables. Without the grant these names are unbound.

| | |
|---|---|
| `(spawn thunk)` | start `thunk` (a procedure of no arguments) as a new task and return the task at once |
| `(task-wait t)` | wait for `t` to finish; its value, or the error it ended with raised again here |
| `(task-wait-all ts)` | `task-wait` for each task in the list; the list of their values. If any failed, the first one's error is raised once all have finished |
| `(task-done? t)` | `#t` once `t` has finished; never waits |
| `(task-cancel! t)` | cancel `t`: it stops at its next step, and a wait or a host call it is in the middle of (an HTTP request, a program) ends. Does nothing to a finished task |
| `(current-task)` | the task running this call |
| `(task? x)` | |

Channels carry values between tasks:

| | |
|---|---|
| `(make-channel)` `(make-channel n)` | a channel. With no argument it is unbuffered: a send waits for a receive. With `n` it holds up to `n` values before a send waits |
| `(channel-send! ch v)` | send `v`, waiting until a receiver or buffer room takes it. Raises an error if the channel is closed |
| `(channel-recv ch)` `(channel-recv ch eof)` | receive a value, waiting for one. From a closed, empty channel it raises an error, or returns `eof` if one was given |
| `(channel-try-recv ch fallback)` | a ready value, else `fallback`; never waits |
| `(channel-close! ch)` | close it: receivers still get what it holds, then find it closed, and every task waiting on it wakes. Closing twice is an error |
| `(channel-closed? ch)` `(channel? x)` | |
| `(channel-for-each f ch)` | call `f` on each value until the channel is closed and empty |
| `(on-recv ch)` `(on-recv ch eof)` `(on-send ch v)` | cases for `select` |
| `(select cases)` `(select cases default)` | wait until one case can proceed and do it; `(index . value)`, where `value` is what was received (`#t` for a send). When several can proceed, one is picked at random, so none starves. With `default`, a procedure of no arguments, it never waits: if no case can proceed the result is `(-1 . (default))` |
| `(after seconds)` | a channel that receives `#t` once, after that long. For timeouts: `(select (list (on-recv work) (on-recv (after 5))))` |

```lisp
(define jobs (make-channel 8))
(define results (make-channel 8))
(define workers
  (map (lambda (id)
         (spawn (lambda () (channel-for-each (lambda (n) (channel-send! results (* n n))) jobs))))
       (iota 4)))
```

Waiting for several things, and sharing data safely:

| | |
|---|---|
| `(task-group f)` | call `(f group)`. Tasks started with `(group-spawn group thunk)` belong to the group, and `task-group` waits for all of them before it returns `f`'s value. The first of them to fail cancels the rest, and its error is the one raised |
| `(pmap f xs)` | `map` with the calls running at the same time; results are in order. The first call to fail cancels the others and its error is raised. It uses as many tasks as the host's task limit leaves room for, the workers sharing the items |
| `(pmap-limit n f xs)` | the same with at most `n` calls running at once: the form for a thousand URLs |
| `(pfor-each f xs)` | `for-each` with the calls running at the same time |
| `(race thunks)` | run the procedures at the same time and return the first value any returns; the rest are cancelled. A procedure that fails only drops out; if none returns a value, the first error is raised |
| `(with-timeout seconds thunk)` | call `thunk` with a time limit. At the limit it is cancelled, including a request or program it is in the middle of, a `guard` inside it cannot catch that, and the caller gets an ordinary error `timed out after …` |
| `(make-mutex)` `(with-lock m thunk)` | a lock, and a call made while holding it. The lock is released however `thunk` ends, so it cannot be left held. It is not reentrant: taking a lock you hold is a deadlock |
| `(make-atom v)` `(atom-ref a)` `(atom-set! a v)` | a cell tasks can read and replace without tearing it |
| `(swap! a f)` | replace the value with `(f value)` and return it, atomically: no other update is lost. `f` runs with nothing held, and again on the new value if another task changed the atom meanwhile, so it may run more than once and should have no effects of its own |
| `(compare-and-set! a old new)` | store `new` only if the value is `eqv?` to `old`; whether it did |
| `(make-once)` `(once-do o thunk)` | run `thunk` unless a task already has, and return its value, the same for every caller. A task that asks while another is running it waits. If it fails nothing is remembered |

```lisp
(define counts (make-hash))
(pfor-each (lambda (word) (hash-update! counts word (lambda (n) (+ n 1)) 0)) words)

(define total (make-atom 0))
(pfor-each (lambda (n) (swap! total (lambda (t) (+ t n)))) (iota 1000))

(with-timeout 5 (lambda () (http-request "GET" "https://example.com/slow")))
```

Rules:

- **A task lives no longer than the evaluation that started it.** When the outermost `EvalString`, `Apply` or `LoadFile` returns, tasks still running are cancelled and waited for first. A task that failed and that nothing waited for fails the evaluation instead of being dropped; a script that wants fire-and-forget catches the error inside the thunk.
- **Deadlock is an error.** When every task is waiting for another (on a channel, or in `task-wait`) and nothing, such as a timer or a task in a host call, can wake any of them, the task that started the evaluation gets `deadlock: all tasks are waiting for one another and none can run`, an ordinary error `guard` can catch, instead of a hang.
- **An ordinary error in a task** is raised again by `task-wait`, where `guard` can catch it.
- **Cancellation** cannot be caught by the task it cancels, as with a host's cancelled context. `task-wait` on a cancelled task raises an ordinary error ("task N was cancelled").
- **A denial from the guard, or a halt, in any task ends the whole evaluation** and cannot be caught by `guard` anywhere, including around `task-wait`. A script cannot hide a refusal by running the refused call in a task.
- **Memory.** Each operation on a variable, vector or hash table is atomic: tasks cannot corrupt them, and the host cannot crash. A *sequence* of operations is not: `(set! n (+ n 1))` reads `n` and then writes it, and another task's write in between is lost. `hash-update!` applies its procedure atomically (it may run it more than once, so it should have no effects). The primitives for the compound cases come with channels, locks and atoms.
- **Output.** `with-output-to-string` captures only the task that asked for it. A spawned task writes to the host's output, not to its spawner's capture.
- **Origin and taint** are inherited: a task started from a model-originated call is model-originated, and a value keeps its provenance through `task-wait`.
- **Limits.** The host bounds the tasks alive at once (default 256) and how deeply each may nest (default 2,500); past the first, `spawn` raises an error.

### JSON

| | |
|---|---|
| `(json-parse s)` | JSON text → value |
| `(json-emit v)` | value → compact JSON text |

Mapping, in both directions:

| JSON | Scheme |
|---|---|
| object | association list with **string** keys, in document order: `(("a" . 1) ("b" . 2))` |
| array | list |
| string / number / `true` / `false` | string / integer-or-float / `#t` / `#f` |
| `null` | the symbol `null` |

`json-emit` treats a proper list whose elements are all pairs with string
cars as an object and any other proper list as an array. Consequently `'()`
emits `[]`, there is no way to emit `{}`, and parsed JSON does not always
come back as it was: an array of arrays that each start with a string
(`[["a","b"]]`) parses to a list that emits as an object (`{"a":["b"]}`). A Go
host that knows the type it wants writes the top level against it
(`scheme.ToJSONAs`): `"list"` is an array, even of elements that look like an
object's entries, and `"alist"` an object, `{}` when empty. Procedures,
handles, vectors, void, symbols other than `null`, and a NaN or infinite
float cannot be emitted.

### Provenance

Every value carries **provenance tags**: a set of strings saying where it
came from, which can differ from what it contains. The interpreter mints none of its own. A host
primitive declares the tag its result carries (`Meta.TaintSource`, such as
`external:file`), a handle carries `cred:<class>`, and the tags of a call's
arguments flow into its result.

| | |
|---|---|
| `(taint-of x)` | the tags as a list of strings, sorted; `()` for a clean value |
| `(tainted? x)` | true if the value carries any tag |
| `(wipe x)` | the same value with its tags removed. Hidden, and audited. The interpreter lets any program call it; what keeps a program from doing so is a gate that refuses a primitive with `Meta.TaintStrip` to an evaluation whose origin is not the user's (§8) |

Propagation happens at the primitive dispatch. A list keeps each element's
tags on the element, and a list built without a primitive (by `map`, by
quasiquote, or holding what `vector-set!` put in a vector) may carry none of
its own; so a string a primitive makes from a list or a vector (`string-join`,
`json-emit`) takes the tags of everything in it, hash tables included (keys
and values), as do the output
`with-output-to-string` captures and the message `guard` hands on from an
error that quoted a tagged value. A primitive whose result *measures* its
arguments rather than being made of them carries nothing (`Meta.TaintOpaque`):
`length`, `string-length`, `vector-length`, every predicate (`null?`,
`string?`, `even?`, …), the comparisons (`=`, `<`, `string=?`,
`string-contains?`, …), `not`, and `taint-of` itself.

What provenance is for: a gate can refuse to send content that came from
outside to a destination nobody authorised. What it is **not**: a boundary
against the person who writes the program, who may `wipe`, and it does not
survive a value being rebuilt a character at a time through the opaque
primitives above. A host that wants a line it can rely on makes the line in
its gate and its primitives, and treats the tags as a way to notice an
accident.

### Meta

| | |
|---|---|
| `(primitives)` | list of primitive names, sorted |
| `(primitive-info name)` | alist: `doc`, `params`, `variadic`, `egress`, `irreversible`, `host`, `origin` |
| `(register-tool name proc meta)` | add a primitive backed by a Scheme procedure; see §7 |
| `(load path)` | evaluate a file's forms in the global environment; a relative path resolves against the interpreter's base directory (`scheme.WithBaseDir`). A **host** primitive: it reads the filesystem, and a gate may refuse it |
| `(handle? x)` | |

## 6. Prelude

`scheme/prelude/*.scm` is embedded in the binary and loaded into the global
environment before anything else. It defines, in Scheme:

| | |
|---|---|
| `(assoc-ref al key [default])` | `cdr` of `(assoc key al)`, or `default` (void if absent) |
| `(identity x)` | |
| `(compose f g)` | `(lambda args (f (apply g args)))` |
| `(first l)` `(second l)` `(third l)` `(rest l)` | `car`, `cadr`, `caddr`, `cdr` |
| `(take l n)` `(drop l n)` `(last l)` | first `n` elements, all but the first `n`, the final element; an error when the list is too short or, for `last`, empty |
| `(any pred l)` `(every pred l)` | the first true value `pred` gives (`#f` if none) / `pred`'s last value if it is true for every element (`#t` for `()`) |
| `(count pred l)` `(list-index pred l)` | how many elements satisfy `pred` / the index of the first, or `#f` |
| `(remove pred l)` `(delete x l)` `(delete-duplicates l)` | `l` without the elements that satisfy `pred` / that are `equal?` to `x` / that repeat an earlier one |
| `(partition pred l)` | a list of two lists: the elements that satisfy `pred`, then the rest |
| `(filter-map f l)` `(append-map f l)` | `map`, keeping the true results / appending the list results |
| `(string-index s sub)` | the rune index of the first occurrence of `sub` in `s`, or `#f` |
| `(string-pad-left s n [pad])` `(string-pad-right s n [pad])` | add `pad` (default `" "`) at the left or right until `s` is at least `n` runes long; never cuts |
| `(vector-map f v)` `(vector-for-each f v)` | `map` and `for-each` over one vector (`vector-map` makes a new vector) |
| `(string-empty? s)` | |

## 7. Primitives and the gate

The **primitive table** maps a name to a Go handler plus metadata. It is the
only surface through which Scheme reaches the host.

Every primitive declares:

| Field | Meaning |
|---|---|
| `doc` | one line; required |
| `params` | ordered list of `(name type doc)`; `type` is one of `string integer number boolean list alist vector any`. A Go primitive may mark trailing parameters optional: a call may stop before any of them, and the handler sees fewer arguments; one skipped before a later one that is given gets its type's empty value. A variadic primitive cannot also have optional parameters. A Go primitive's list parameter whose elements are objects may describe their fields (`Items`): the tool schema gives it an `items` object schema, and the gate can read a field's role as it reads a parameter's |
| `variadic` | extra arguments allowed after the declared ones |
| `egress` | may send bytes off-machine |
| `irreversible` | cannot be undone by the host |
| `host` | touches the OS (filesystem, processes, network) |
| `origin` | `builtin` or `scheme` |
| `hidden` | not listed by `Interp.Tools` (`load`, `register-check`, `wipe`) |
| `audited` | a gate should record its decision even when it is an allow |
| `preview` | Go-only, for irreversible host primitives: renders what the call would do (a diff) for a gate that asks |

The interpreter itself acts on `params`, `variadic`, `hidden`, `host`, `origin`
and the taint fields (`TaintSource`, `TaintOpaque`, `TaintStrip`); the
`egress`, `irreversible` and `audited` flags, the `Egress*` and `ConfirmReason`
fields, `preview` and the parameter **roles** (`path`, `source`, `dest`,
`payload`, `credential`, and for commands and keyed requests `argv`, `method`
and `position`) are declarations the gate reads. A program written for a host
that has no gate loses nothing by ignoring them.

**Application of a primitive**, whether from the host, the prelude, a `.scm`
file or the REPL, goes through exactly one Go function, which:

1. checks the argument count against `params`/`variadic`;
2. asks the **gate** (when one is installed) for a decision; a denial raises
   `denied: <reason>`;
3. calls the handler.

`register-tool` adds a primitive whose handler applies a Scheme procedure:

```lisp
(register-tool "square-all"
  (lambda (xs) (map (lambda (x) (* x x)) xs))
  '((doc . "Square every number in a list")
    (params . (("xs" "list" "the numbers")))
    (irreversible . #f)))
```

Rules: the name must be new (builtins cannot be shadowed, and a tool cannot
be re-registered, so the list of primitives a host offers stays stable); once
the host has called `Interp.Freeze`, `register-tool` is an error wherever it
is called from. `doc` and `params` are required; `egress`, `irreversible` and
`variadic` default to `#f`. What a provider or the first call would reject is
refused where the tool is registered: a name outside 1 to 64 letters, digits,
`-` and `_`, a parameter listed twice, and a procedure whose arguments do not
fit `params` (a variadic tool needs a rest parameter). A registered tool is
indistinguishable from a builtin at the dispatch site, and its own primitive
calls pass the gate in turn.

`register-check` adds a gate checker written in Scheme. It is hidden and
needs a gate that implements `RegisterChecker` (without one it is an error),
and it is refused to an evaluation whose origin is not the user's (§8):

```lisp
(register-check "no-external-llms" "confirm"
  (lambda (prim-name args)
    (or (equal? prim-name "shell-exec")
        (equal? prim-name "http-request"))))
```

- `name` is the checker name the gate reports.
- `verdict` is fixed to `"deny"` or `"confirm"` at registration time. A
  checker can only tighten what the gate decides, never relax it; that
  contract is the gate's to keep, and the interpreter passes the verdict
  through.
- `predicate` is a procedure `(prim-name args)`; the gate decides what a true
  value and an error mean.

Loading a `.scm` file can grow the primitive table and the global
environment. It **cannot** add special forms or change evaluation: `eval`
and `apply` are two fixed Go functions, and the test suite checks, after loading
arbitrary code, that the primitive table grew by the registered tools alone
and that `define-syntax` is still a plain binding.

## 8. Embedding API (Go)

```go
in := scheme.New(scheme.WithOutput(w))       // prelude loaded
v, err := in.EvalString(ctx, "(+ 1 2)")      // last value of the source
v, err = in.Apply(ctx, proc, args)           // call a procedure
err = in.Register(&scheme.Prim{...})         // add a host primitive
in.SetGate(g)                                // install the gate
snap := in.Global().Snapshot()               // copy the global bindings
in.Global().Restore(snap)                    // put them back, unbinding names bound since
```

`ctx` cancellation is checked at every primitive application; a cancelled
evaluation returns the context's error. `ctx` also says who started the
evaluation (`scheme.WithOrigin`: `OriginUser`, `OriginModel` or
`OriginHarness`); a context that does not say is `OriginHarness`, never the
user's, so an embedder gives the user's powers (`wipe`, `register-check`) only
where it says so. The interpreter attaches one meaning to the origin:
`register-check` is refused unless it is the user's. A gate may attach more.

The rest (lifecycle, errors, cancellation, concurrency, writing a host
primitive, writing a gate) is in [embedding.md](embedding.md).

## 9. Not in Brote Scheme

Deliberately absent.

- characters and the full string-port API (only `with-output-to-string`)
- source positions in runtime errors (reader errors carry positions)
- `define-macro`, which the prelude does not need
- `set-car!` / `set-cdr!` and string mutation
- nested quasiquote
- hygienic macros (`define-syntax`, `syntax-rules`)
- `call/cc`, `dynamic-wind`
- an `eval` primitive: the kernel surface is not exposed to programs
- exact rationals and bignums
