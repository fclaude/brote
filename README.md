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
brote FILE -- a b     # what follows -- is for the script: (command-line) gives ("a" "b")
brote -version
```

`brote` has the language's builtins and `load`, and three primitives for a
script that needs something of the machine: `(command-line)` is the arguments
after `--` as a list of strings, `(read-file path)` is a file's contents as a
string, and `(read-line)` is the next line of standard input without its line
ending, or `#f` once there is no more (it is not there in the REPL, which reads
standard input itself). They belong to the command: the `scheme` package an
embedding program imports has no host. `brote` still cannot run a program or
open a socket. It is for trying the language, running small programs, and
checking what a host would run. An error goes to standard error and ends a file
or `-e` run with status 1. There is no line editing; `rlwrap brote` adds it.

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

The API has two layers. The core above is all an embedder needs; the rest
(tool presentation, provenance tags, credential handles, origin, reload) is
what Brote's first host, a coding agent, needed, and is marked as such in
[What is Nodwyn's](docs/embedding.md#what-is-nodwyns).

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
