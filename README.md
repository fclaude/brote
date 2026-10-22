# Brote

[![CI](https://github.com/fclaude/brote/actions/workflows/ci.yml/badge.svg)](https://github.com/fclaude/brote/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/fclaude/brote.svg)](https://pkg.go.dev/github.com/fclaude/brote/scheme)

A small Scheme for embedding in Go programs, and `brote`, the command that
runs it on its own.

Brote is a Go module with no dependencies outside the standard library. The
language is deliberately small (int64 and float64 numbers, UTF-8 strings,
lists, vectors, closures, proper tail calls, `guard`, JSON) and reaches the
machine only through the Go functions a host registers, plus `load`, which a
host can refuse. Evaluation is cancellable through a `context.Context`,
nesting is bounded, and a host can put a gate in front of every call from
Scheme into Go.

```scheme
(define (fib n)
  (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))
(map fib (iota 10))        ; (0 1 1 2 3 5 8 13 21 34)
```

## Install

```sh
go install github.com/fclaude/brote/cmd/brote@latest   # the command
go get github.com/fclaude/brote                        # the library
```

Go 1.26 or newer.

## The `brote` command

```sh
brote                 # a REPL on standard input (prompts and Ctrl-C when it is a terminal)
brote FILE...         # run files, in order, in one interpreter
brote -e '(+ 1 2)'    # evaluate an expression and print its value
brote -version
```

`brote` has the language's builtins and `load`, and no other way to reach the
machine: it cannot read a file into a string, run a program or open a socket.
It is for trying the language, running pure programs, and checking what a host
would run. An error goes to standard error and ends a file or `-e` run with
status 1. There is no line editing; `rlwrap brote` adds it.

## Embedding

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

An interpreter can keep closures between evaluations, call back into Scheme
from a Go handler, be stopped by its context, and run under a gate that
refuses primitives. Separate interpreters share nothing; one interpreter is
used by one goroutine at a time. [docs/embedding.md](docs/embedding.md) is the
contract: lifecycle and ownership, values, writing host operations, errors,
cancellation, gates. `go doc github.com/fclaude/brote/scheme` has runnable
examples.

## Documentation

- [docs/scheme.md](docs/scheme.md): the language reference. A builtin that is
  not documented there fails the test suite.
- [docs/embedding.md](docs/embedding.md): embedding in Go.
- [PROVENANCE.md](PROVENANCE.md): where the code came from and what changed.

## Development

```sh
make check    # gofmt, go vet, staticcheck if installed, go test -race ./...
make build    # bin/brote
```

The tests in `scheme/` are the language's conformance suite plus the embedding
contract (`embed_test.go`); the examples in `scheme/example_test.go` are
tests too, and `FuzzReader` fuzzes the reader.

## Status

Brote is pre-1.0 and its API may change. It was extracted, without changing
its behaviour, from the interpreter of Nodwyn, a coding agent, and its API
still carries what Nodwyn needed (provenance tags, egress declarations, tool
schemas); [docs/embedding.md](docs/embedding.md) lists them under "What is
Nodwyn's" so an embedder knows what to ignore. They will move behind narrower
interfaces once another host shows which of them it uses. The
[Coigüe](https://github.com/fclaude/coigue) editor embeds it for its
extension commands.

## Licence

[MIT](LICENSE).
