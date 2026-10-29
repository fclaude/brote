# The brote command

`brote` is the interpreter on its own, for trying the language or running a script. It's the same interpreter a Go program embeds, with three extra procedures that a script needs to get at its arguments, a file and standard input, and with [tasks and channels](scheme.md#tasks) and the [extended library](std.md) (files, HTTP, programs, the clock, regular expressions and more) switched on.

```console
brote                 a REPL on standard input
brote FILE...         run files, in order, in one interpreter
brote -e EXPR         evaluate an expression and print its value
brote FILE -- ARG...  everything after -- is for the script
brote -sandbox FILE   run with no access to files, network or programs
brote -allow SPEC ... run with only what SPEC names
brote -version
```

## The REPL

With nothing to run, `brote` reads forms from standard input and prints the value of each one, unless it's void. When standard input is a terminal you get a `brote> ` prompt and Ctrl-C stops the form that's running without ending the session. There's no line editing. If you want history and arrow keys, `rlwrap brote` adds them.

The same thing happens when you pipe text in, just without the prompt:

<!-- run-shell -->
```console
$ printf '(define x 6)\n(* x 7)\n(string-append "a" "b")\n' | brote
42
"ab"
```

An error is printed and the REPL carries on with the next form.

## Expressions and files

`-e` evaluates one expression and prints its value, which makes `brote` usable as a calculator or in a pipeline:

<!-- run-shell -->
```console
$ brote -e '(map (lambda (n) (* n n)) (iota 5))'
(0 1 4 9 16)
$ brote -e '(define x 1)'
$ brote -e '(car 5)'
brote: car: argument 1: expected pair, got integer 5
```

The last one printed to standard error and ended with status 1. That's what an error does in a file or with `-e`: the message goes to standard error and the exit status is 1. Files run in the order you give them, all in one interpreter, so a later file can use what an earlier one defined. They print only what the program prints with `display`, `write` and `newline`.

<!-- run-shell -->
```console
$ printf '(define greeting "hello")\n' > a.scm
$ printf '(display greeting) (newline)\n' > b.scm
$ brote a.scm b.scm
hello
$ echo $?
0
```

## Arguments, files and standard input

A script that needs to read something has three procedures to work with. They belong to the command, so a Go program that embeds Brote doesn't get them unless it registers its own.

| Procedure | What it gives back |
|---|---|
| `(command-line)` | the arguments after `--` as a list of strings, `()` when there are none |
| `(read-file path)` | the contents of the file as a string. A file that can't be read is an error that names it |
| `(read-line)` | the next line of standard input without its line ending, or `#f` when there's no more. Not available in the REPL, which reads standard input itself |

Everything after `--` belongs to the script, even if it looks like one of `brote`'s own flags:

<!-- run-shell -->
```console
$ brote -e '(command-line)' -- one "two words" -version
("one" "two words" "-version")
```

Here's a small script that counts the lines in the files it's given. It reads each file with `read-file` and splits it on newlines:

<!-- run-shell -->
```console
$ printf '(for-each (lambda (path)\n  (display path) (display ": ")\n  (display (length (string-split (string-trim (read-file path)) "\\n")))\n  (newline))\n  (command-line))\n' > lines.scm
$ printf 'one\ntwo\nthree\n' > three.txt
$ printf 'just one\n' > one.txt
$ brote lines.scm -- three.txt one.txt
three.txt: 3
one.txt: 1
```

And `read-line` loops until it sees `#f`:

<!-- run-shell -->
```console
$ printf '(let loop ((line (read-line)) (n 0))\n  (if line\n      (begin (display n) (display " ") (display (string-upcase line)) (newline) (loop (read-line) (+ n 1)))))\n' > shout.scm
$ printf 'first\nsecond\n' | brote shout.scm
0 FIRST
1 SECOND
```

## When a script fails

An error in a file says where it happened, and which procedures it was in:

<!-- run-shell -->
```console
$ printf '(define (half x) (/ x 2))\n(define (run x)\n  (+ 1 (car (half x))))\n(run 10)\n' > fail.scm
$ brote fail.scm
brote: fail.scm:3:8: car: argument 1: expected pair, got integer 5
  in run (fail.scm:3:3)
```

The first line is the position of the form that failed, then the message. Each line after it is a procedure the error left, innermost first, with the place in it where evaluation was. A procedure that ended in a tail call is not listed. The REPL gives the same when a form fails inside a procedure.

A file that doesn't exist is an error like any other:

<!-- run-shell -->
```console
$ brote -e '(read-file "missing.txt")'
brote: read-file: missing.txt: no such file or directory
```

## What a script can reach

`brote` runs your own scripts with your own rights, so by default a script gets every module of the [extended library](std.md): it can read and write files, make HTTP requests, run programs and read the environment, as you can. The options below take things away. They are how you run a script you did not write, or one you want to keep to its job. The [capabilities page](capabilities.md) explains the model and the policy language.

| Option | Meaning |
|---|---|
| `-sandbox` | pure computation, the clock and randomness, and nothing that reaches the machine |
| `-allow SPEC` | grant only what is named; repeatable. `fs.read=DIR`, `fs.write=DIR`, `net=HOST`, `exec=COMMAND`, `env=NAME`, or a module name such as `regexp` |
| `-deny MODULE` | remove a module from the default; repeatable |
| `-policy FILE` | start from the policy in `FILE` |
| `-print-policy` | show what would be granted and exit |

Naming something with `-allow` switches the start from everything to the sandbox, so the script gets what you named and no more. `-deny` goes the other way and subtracts from everything.

<!-- run-shell -->
```console
$ printf 'hello\n' > in.txt
$ brote -allow fs.read=. -e '(read-file "in.txt")'
"hello\n"
$ brote -allow fs.read=. -e '(read-file "/etc/hosts")'
brote: read-file: /etc/hosts is outside the directories this host lets scripts read
$ brote -sandbox -e '(read-file "in.txt")'
brote: eval: unbound variable: read-file
$ brote -deny net -deny exec -e '(string-length (read-file "in.txt"))'
6
```

When one of these options is given, `load` is also held to the files the script may read. A file named on the command line is always run: you chose it.
