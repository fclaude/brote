# Brote

[![CI](https://github.com/fclaude/brote/actions/workflows/ci.yml/badge.svg)](https://github.com/fclaude/brote/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/fclaude/brote.svg)](https://pkg.go.dev/github.com/fclaude/brote/scheme)

Brote is a small Scheme interpreter written in Go. A Go program embeds it to let people script the program, and the `brote` command runs it on its own. It came out of the interpreter inside my coding agent, which I pulled out so other programs could use it.

The language has 64-bit integers and floats, UTF-8 strings, lists, vectors, hash tables, closures, proper tail calls, `guard` for errors, and JSON. It has no dependencies outside the Go standard library. A script can only reach the machine through the Go functions the host program registers, evaluation stops when its `context.Context` does, and a host can put a gate in front of every call from Scheme into Go. The `std` package has files, HTTP, programs, regular expressions and more behind a policy: a host grants a module, and how much of it, or the script doesn't have it at all.

Here's a program and what the interpreter prints for it:

<!-- run -->
```lisp
(define (fib n)
  (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))
(map fib (iota 10))
```

```text
(0 1 1 2 3 5 8 13 21 34)
```

## Install

```bash
go install github.com/fclaude/brote/cmd/brote@latest   # the command
go get github.com/fclaude/brote                        # the library
```

You need Go 1.26 or newer. Run `brote` with no arguments for a REPL.

## Embedding

A host registers primitives, and Scheme code calls them by name:

```go
in := scheme.New(scheme.WithOutput(os.Stdout))
in.Register(&scheme.Prim{
    Name: "shout",
    Meta: scheme.Meta{Doc: "Upper-case a string.", Params: []scheme.Param{{Name: "text", Type: "string"}}},
    Handler: func(c *scheme.Call) (scheme.Value, error) {
        s, err := c.String(0)
        if err != nil {
            return scheme.Value{}, err
        }
        return scheme.Str(strings.ToUpper(s)), nil
    },
})
v, err := in.EvalString(ctx, `(shout "hello")`) // "HELLO"
```

## Documentation

The documentation is at <https://fclaude.github.io/brote/>, and the same pages are in `docs/`:

- [A tour of Brote](docs/tour.md) teaches the language by typing things in.
- [Standard library](docs/stdlib.md) lists the procedures for lists, strings, vectors and hash tables.
- [Extended library](docs/std.md) covers files, HTTP, programs, the environment, time, randomness, regular expressions, encodings and digests.
- [Capabilities](docs/capabilities.md) is how a host, or the `brote` command, chooses which of those a script gets, and how plugins ask for theirs.
- [The brote command](docs/command.md) covers arguments, files and standard input.
- [Language reference](docs/scheme.md) is the full description of the syntax and the builtins. A builtin that isn't documented there fails the test suite.
- [Embedding in Go](docs/embedding.md) covers lifecycle, values, host operations, errors and gates.
- [PROVENANCE.md](PROVENANCE.md) says where the code came from and what changed.

## Development

```bash
make check    # gofmt, go vet, staticcheck if installed, go test -race ./...
make build    # bin/brote
```

The tests in `scheme/` are the language's conformance suite and the embedding contract (`embed_test.go`). The examples in `scheme/example_test.go` are tests too, `FuzzReader` fuzzes the reader, and `cmd/brote/docs_test.go` runs every example on the documentation pages and compares what they print with what the page shows. After changing a page, `DOCS_UPDATE=1 go test ./cmd/brote -run TestDocExamples` rewrites the output blocks.

## Status

Brote is pre-1.0 and its API may change. Its first host was my coding agent, Nodwyn, so the API still carries some of what Nodwyn needed (provenance tags, egress declarations, tool schemas). The [embedding guide](docs/embedding.md) lists them under "What is Nodwyn's" so you know what to ignore. The [Coigüe](https://github.com/fclaude/coigue) editor embeds Brote for its extension commands.

## Licence

[MIT](LICENSE).

## Language server

`brote-lsp` provides static Scheme diagnostics, lexical navigation, builtin hover
and completion over stdio. Embedding hosts supply generated API metadata without
forking the server. See [language server](docs/language-server.md) for setup, the
host-profile format, and current limits.
