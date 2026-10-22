# Brote Scheme — language reference

Brote is a small Scheme for embedding in a Go program. A program written in it
decides *what to do*; every effect — reading a file, running a command, calling
a service — is a Go **primitive** that the host registers, and every primitive
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

```scheme
(define pattern """\d+\.\d+""")
```

When the opening delimiter is followed immediately by a newline the block is
read as **indented text**: that first newline is dropped, the indentation
shared by every non-blank line and by the closing delimiter's line is
removed, and the newline before the closing delimiter is dropped. A block
therefore reads at the indentation it is written at and produces text with
none:

```scheme
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
| procedure | `#<procedure f>` `#<primitive car>` | `procedure?` | closures and primitives are both procedures |
| handle | `#<cred:class:id>` | `handle?` | opaque value a host primitive hands out: a program can pass it around and compare it, never open it. No builtin converts it to a string, and everything derived from it carries its class as a provenance tag (§5) |
| void | `#<void>` | — | result of `define`, `set!`, `display`, …; the REPL does not print it |

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
| `(do ((v init [step]) …) (test [e …]) cmd …)` | loop: check `test`; on false evaluate the `cmd`s, then the `step`s (in parallel), rebind, repeat. Value is the test clause's last `e` (void if none). A Go loop — constant stack |
| `(when test e …)` | `e …` only if `test` is true; yields void otherwise |
| `(unless test e …)` | `e …` only if `test` is false; yields void otherwise |
| `(guard (var clause …) body …)` | evaluate `body …`; if it raises a Scheme error, bind `var` to the condition — an alist `((message . "text") (irritants …))` — and try the clauses like `cond` (`(test body …)`, `(test => proc)`, `(else …)`). The first true clause's value is the value of `guard`; no match re-raises. A guard **denial** and a **cancellation** are never catchable |
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

```scheme
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

### Strings

| | |
|---|---|
| `(string? x)` `(string-length s)` | length in runes |
| `(string-append s …)` | |
| `(substring s start [end])` | rune indices; `end` defaults to the length; out of range is an error |
| `(string=? a b …)` `(string<? a b …)` | |
| `(string-upcase s)` `(string-downcase s)` `(string-trim s)` | `string-trim` strips Unicode whitespace at both ends |
| `(string-split s sep)` | `sep` is a literal separator, not a regex; empty `sep` is an error |
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
came from, not what it contains. The interpreter mints none of its own. A host
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
`json-emit`) takes the tags of everything in it, as do the output
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

**Application of a primitive** — whether from the host, the prelude, a `.scm`
file or the REPL — goes through exactly one Go function, which:

1. checks the argument count against `params`/`variadic`;
2. asks the **gate** (when one is installed) for a decision; a denial raises
   `denied: <reason>`;
3. calls the handler.

`register-tool` adds a primitive whose handler applies a Scheme procedure:

```scheme
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

```scheme
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

The rest — lifecycle, errors, cancellation, concurrency, writing a host
primitive, writing a gate — is in [embedding.md](embedding.md).

## 9. Not in Brote Scheme

Deliberately absent.

- characters and the full string-port API (only `with-output-to-string`)
- source positions in runtime errors (reader errors carry positions)
- `define-macro` — the prelude does not need it
- `set-car!` / `set-cdr!` and string mutation
- nested quasiquote
- hygienic macros (`define-syntax`, `syntax-rules`)
- `call/cc`, `dynamic-wind`
- an `eval` primitive — the kernel surface is not exposed to programs
- exact rationals and bignums
