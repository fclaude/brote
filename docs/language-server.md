# Brote language server

`brote-lsp` speaks LSP over standard input/output. Install it alongside `brote`:

```sh
go install github.com/fclaude/brote/cmd/brote-lsp@latest
```

It accepts Scheme (`.scm`) documents using full-document synchronization and
UTF-16 positions. It reports reader errors, lists document symbols, resolves
lexical definitions and references, and searches symbols in open documents.
Hover and completion include Brote builtin signatures and documentation.
Lexical analysis covers definitions, lambda parameters, let/let*/letrec (including
named let), do, guard, quoted data, and quasiquote/unquote. Broken input retains
partial syntax so navigation remains useful while a form is unfinished.

Analysis never evaluates source, loads a file, or invokes a host operation.
The source-preserving reader is `scheme.ReadSyntax`; reusable analysis is in
`analysis`. Runtime reading and syntax diagnostics share the same reader rules.

## Embedding host metadata

An embedding editor sends a data-only profile in the initialize request:

```json
{"initializationOptions":{"hostProfile":{"name":"my-editor","symbols":[
  {"name":"message","doc":"Display text","parameters":["text"]}
]}}}
```

Generate this from the host's actual primitive declarations. No host-specific
code belongs in the server. The profile applies to the server session, so use
separate sessions for host configuration files and unrelated Scheme files.

## Current limits

Navigation is within each document; workspace search and rename index every
`.scm` file under the `rootUri` the client sent at initialize (bounded at 2000
files of 1 MB, hidden directories skipped), open documents winning, so they
reach files that were never opened. An indexed file that changed on disk is
read again before it is searched or renamed in; files created after
initialize are not indexed. Host-specific module imports, dynamic
bindings, macros and inferred types are not resolved. Unknown names are not
errors. The server advertises `textDocument/formatting` (the same
re-indentation `brote fmt` gives), `textDocument/rename` (for a parameter or local
binding, its uses in that document; for a top-level name, its top-level
definitions and the uses that resolve to them in every indexed file, never a
local binding of the same name) and `textDocument/semanticTokens/full`
(functions, variables, builtins and keywords, per the advertised legend); it
does not advertise signature help. Coigüe currently uses its diagnostics and
navigation; completion and hover need editor UI support.

Run `python3 scripts/lsp-smoke.py /path/to/brote-lsp` to exercise the packaged
server's real stdio connection and host-profile support.

## Warnings about tasks

Besides reader errors, the server warns (severity "warning") about the shapes of code that tasks make easy to get wrong:

- a `set!` of a variable that other tasks can see, inside a thunk given to `spawn`, `group-spawn`, `pmap`, `pmap-limit`, `pfor-each` or `race`. Another task's write can be lost, and when the new value is computed from the old one the message says so; use an atom (`make-atom`, `swap!`) or `with-lock`;
- a call that waits for another task (`channel-recv`, `task-wait`, `select`, `pmap`, and so on) inside a `with-lock` procedure, since that task may be waiting for the lock;
- a side effect (`display`, any procedure ending in `!` other than `set!`) inside the procedure given to `swap!` or `hash-update!`, which may call it more than once.

They look at the shape of the text and never run it, so a procedure passed by name is not examined.
