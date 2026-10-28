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

Navigation is within each document; workspace search covers open documents only.
Host-specific module imports, dynamic bindings, macros and inferred types are
not resolved. Unknown names are not errors. The server does not advertise
formatting, rename, signature help or semantic tokens. Coigüe currently uses
its diagnostics and navigation; completion and hover need editor UI support.

Run `python3 scripts/lsp-smoke.py /path/to/brote-lsp` to exercise the packaged
server's real stdio connection and host-profile support.
