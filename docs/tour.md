# A tour of Brote

This page walks through the language by typing things in. Run `brote` with no arguments and you get a prompt where you can paste any of the examples below. Each one shows what you type, and underneath it what comes back. (The install steps are in the [README](../README.md).)

Brote prints the value of every form you give it, except the ones that have no value worth showing, like `define`. Anything a program prints with `display` shows up in the same place.

## Numbers and text

Arithmetic is prefix: the operator comes first, inside the parentheses.

<!-- run -->
```lisp
(+ 1 2)
(* 3 (+ 4 5))
(/ 7 2)
(/ 8 2)
(sqrt 2)
```

```text
3
27
3.5
4
1.4142135623730951
```

Two integers divide to an integer when they divide evenly, and to a float when they don't. Integers are 64 bits wide and wrap around on overflow, the way they do in Go.

Strings are in double quotes and `string-append` joins them:

<!-- run -->
```lisp
(string-append "scheme" " " "in " "go")
(string-length "héllo")
(string-upcase "quiet")
(number->string 42)
```

```text
"scheme in go"
5
"QUIET"
"42"
```

`string-length` counts runes, so the `é` is one.

## Names and procedures

`define` gives a value a name. Giving a function a name looks like the call you'll make later:

<!-- run -->
```lisp
(define greeting "hello")
(define (square n) (* n n))
(square 12)
(string-append greeting ", " "world")
```

```text
144
"hello, world"
```

`define` printed nothing, as promised. A procedure is a value too, and `lambda` makes one without a name:

<!-- run -->
```lisp
(define add3 (lambda (x) (+ x 3)))
(add3 4)
((lambda (a b) (list b a)) 1 2)
square
```

```text
7
(2 1)
#<procedure square>
```

## Lists

A list is a chain of pairs. `list` builds one, and `car` and `cdr` take it apart, giving the first element and everything after it:

<!-- run -->
```lisp
(define xs (list 10 20 30))
(car xs)
(cdr xs)
(cons 5 xs)
(length xs)
(append xs (list 40))
```

```text
10
(20 30)
(5 10 20 30)
3
(10 20 30 40)
```

Most of the work happens in procedures that take other procedures. `map` applies one to every element, `filter` keeps the ones that pass a test, and `fold` combines everything into a single value:

<!-- run -->
```lisp
(map square (list 1 2 3 4))
(filter odd? (iota 10))
(fold + 0 (iota 101))
(sort (list 3 1 2) <)
```

```text
(1 4 9 16)
(1 3 5 7 9)
5050
(1 2 3)
```

`iota` counts up from zero, so `(iota 101)` is the numbers 0 to 100. The [standard library](stdlib.md) lists the rest of the list procedures.

## Making decisions

`if` takes a test and two branches. Only `#f` counts as false, so `0` and the empty string are both true:

<!-- run -->
```lisp
(if (> 3 2) "yes" "no")
(if 0 "zero is true" "zero is false")
(define (describe n)
  (cond ((< n 0) "negative")
        ((= n 0) "zero")
        (else "positive")))
(map describe (list -5 0 7))
```

```text
"yes"
"zero is true"
("negative" "zero" "positive")
```

`let` names some intermediate values for the length of a body:

<!-- run -->
```lisp
(let ((a 3) (b 4))
  (sqrt (+ (* a a) (* b b))))
```

```text
5.0
```

## Loops

There's no `while`. A loop is a procedure that calls itself, and Brote runs a call in tail position without growing the stack, so a loop can go for as long as it likes. The named `let` form writes the procedure and its first call in one go:

<!-- run -->
```lisp
(let loop ((i 1) (total 0))
  (if (> i 1000000)
      total
      (loop (+ i 1) (+ total i))))
```

```text
500000500000
```

`do` is there for the loops you'd write with a counter:

<!-- run -->
```lisp
(do ((i 0 (+ i 1)))
    ((= i 3))
  (display "tick ")
  (display i)
  (newline))
```

```text
tick 0
tick 1
tick 2
```

## When something goes wrong

An error that nothing catches stops the program. The REPL prints it and gives you the prompt back:

<!-- run -->
```lisp
(car '())
(undefined-name 1)
(error "something broke:" 42)
```

```text
error: car: argument 1: expected pair, got () ()
error: eval: unbound variable: undefined-name
error: something broke: 42
```

`guard` catches an error and lets you decide what to do. The variable you name holds an association list with the message and the extra values you passed to `error`:

<!-- run -->
```lisp
(guard (e (#t (assoc-ref e 'message)))
  (error "something broke:" 42))
(guard (e ((string-contains? (assoc-ref e 'message) "divi") 0))
  (/ 1 0))
```

```text
"something broke:"
0
```

## A table of counts

Hash tables map keys (strings, symbols, integers and booleans) to values. Here's one counting the words in a sentence:

<!-- run -->
```lisp
(define words (string-split "to be or not to be" " "))
(define counts (make-hash))
(for-each (lambda (w) (hash-update! counts w (lambda (n) (+ n 1)) 0)) words)
(hash->alist counts)
(hash-ref counts "be")
```

```text
(("to" . 2) ("be" . 2) ("or" . 1) ("not" . 1))
2
```

`hash-update!` takes the old value (or the default, `0`, when the key is new) and stores what the procedure returns. The entries come back in the order the keys first appeared. The [standard library](stdlib.md) has the rest of the table procedures.

## Running a file

Put the same forms in a file and pass its name to `brote`. Files print only what the program prints, since there's no prompt to echo values at:

<!-- run-shell -->
```console
$ printf '(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))\n(display (map fib (iota 12)))\n(newline)\n' > fib.scm
$ brote fib.scm
(0 1 1 2 3 5 8 13 21 34 55 89)
```

Anything after `--` on the command line is handed to the program, and that and the other things a script can ask of the machine are on the [command page](command.md).
