# Standard library

Brote's library comes from two places. The builtins are Go functions that every interpreter registers when it starts. The prelude is a few dozen lines of Scheme in `scheme/prelude/` that gets loaded right after. From the REPL you can't tell which is which, except that `(primitives)` lists only the Go ones.

This page covers lists, strings, vectors and hash tables. Numbers, booleans, `format` and JSON are in the [language reference](scheme.md), which also has the special forms and the full list of what Brote leaves out. Every example below is run by the test suite, so the output you see is what you get.

## Lists

Lists are chains of pairs, and they're immutable: nothing changes a pair once it exists. That's why the procedures here all return new lists.

| Procedure | What it gives back |
|---|---|
| `(list x …)` `(cons a d)` `(car p)` `(cdr p)` | the basics, plus `caar`, `cadr`, `cdar`, `cddr`, `caddr` and `cdddr` |
| `(first l)` `(second l)` `(third l)` `(rest l)` | the same as `car`, `cadr`, `caddr` and `cdr`, with names that read better |
| `(length l)` `(reverse l)` `(append l …)` | what you'd expect |
| `(list-ref l i)` `(list-tail l i)` | the element at index `i` (counting from 0) / the list without its first `i` elements |
| `(take l n)` `(drop l n)` | the first `n` elements / everything after them. A list shorter than `n` is an error |
| `(last l)` | the final element. An empty list is an error |
| `(map f l …)` `(for-each f l …)` | apply `f` to each element. `map` collects the results, and with several lists it stops at the shortest |
| `(filter pred l)` `(remove pred l)` | the elements for which `pred` is true / false |
| `(partition pred l)` | a list of two lists: the elements that satisfy `pred`, then the rest |
| `(fold f init l)` | a left fold, calling `(f acc x)` |
| `(filter-map f l)` | `map`, keeping only the results that aren't `#f` |
| `(append-map f l)` | `map` with a function that returns lists, appended together |
| `(any pred l)` `(every pred l)` | `any` gives the first true value `pred` returns, or `#f`. `every` gives the last one if all are true (`#t` for an empty list) |
| `(count pred l)` | how many elements satisfy `pred` |
| `(list-index pred l)` | the index of the first element that satisfies `pred`, or `#f` |
| `(member x l)` `(memq x l)` | the tail of `l` that starts at `x` (compared with `equal?` or `eq?`), or `#f` |
| `(delete x l)` `(delete-duplicates l)` | `l` without the elements `equal?` to `x` / without the repeats, keeping the first of each |
| `(assoc k al)` `(assq k al)` `(assoc-ref al k [default])` | look a key up in an association list. `assoc-ref` gives the value itself |
| `(sort l less?)` `(iota n)` | a stable sort / the list `(0 1 … n-1)` |
| `(apply f arg … l)` | call `f` with the arguments, the last of which is a list |

Let's try a few on a list with some repeats in it:

<!-- run -->
```lisp
(define scores '(3 9 4 9 1))
(take scores 3)
(drop scores 3)
(delete-duplicates scores)
(partition odd? scores)
(count (lambda (n) (> n 3)) scores)
(list-index (lambda (n) (> n 5)) scores)
```

```text
(3 9 4)
(9 1)
(3 9 4 1)
((3 9 9 1) (4))
3
1
```

`any` and `every` return the value the predicate produced, which is handy when the predicate is a lookup:

<!-- run -->
```lisp
(any (lambda (n) (and (> n 5) (* n 10))) scores)
(every (lambda (n) (and (> n 0) n)) scores)
(every even? '())
```

```text
90
1
#t
```

When something goes wrong the REPL prints the error and carries on. Here we ask for more elements than the list has:

<!-- run -->
```lisp
(take scores 8)
```

```text
error: take: the list is shorter than 8
```

## Strings

Strings are immutable UTF-8, and all lengths and indices count runes, which are the characters you'd see on screen. A character is its own type, written `#\a`; `string-ref` gives one, `string->list` gives all of them, and `list->string` and `string` make a string from them.

| Procedure | What it gives back |
|---|---|
| `(string-length s)` `(string-append s …)` | the length in runes / the strings joined |
| `(substring s start [end])` | the part from `start` up to `end`, which defaults to the end |
| `(string=? a b …)` `(string<? a b …)` | comparisons |
| `(string-upcase s)` `(string-downcase s)` `(string-trim s)` | `string-trim` removes whitespace at both ends |
| `(string-split s sep)` `(string-join l sep)` | `sep` is a plain string (no patterns), and it can't be empty when splitting |
| `(string-contains? s sub)` `(string-prefix? prefix s)` `(string-suffix? suffix s)` | true or false. Notice that `string-contains?` takes the big string first and the other two take it last |
| `(string-index s sub)` | the rune index of the first place `sub` appears in `s`, or `#f` |
| `(string-replace s old new)` | every occurrence of `old` replaced |
| `(string-pad-left s n [pad])` `(string-pad-right s n [pad])` | add `pad` (a space unless you give one) on that side until the string is at least `n` runes long. They never cut a long string |
| `(string-ref s k)` `(string->list s)` `(list->string l)` | a character / all the characters as a list / a string from a list of characters |
| `(string-for-each f s)` `(string-map f s)` | call `f` on each character / a string of what `f` returns |
| `(char-upcase c)` `(char-alphabetic? c)` `(char->integer c)` … | the [language reference](scheme.md#characters) lists the character procedures |
| `(string->number s)` `(number->string n)` | `string->number` gives `#f` when the text isn't a number |
| `(string->symbol s)` `(symbol->string sym)` | |
| `(format fmt arg …)` | `~a` displays an argument, `~s` writes it, `~%` is a newline |

Padding makes simple columns:

<!-- run -->
```lisp
(define (row name qty) (string-append (string-pad-right name 8) (string-pad-left (number->string qty) 4)))
(for-each (lambda (r) (display (row (car r) (cdr r))) (newline))
          '(("apples" . 12) ("kiwis" . 3) ("plums" . 140)))
(string-index "hello world" "o w")
(string-split "a,b,,c" ",")
```

```text
apples    12
kiwis      3
plums    140
4
("a" "b" "" "c")
```

Characters make the little jobs easy to write. This reverses the words of a sentence, and counts its vowels:

<!-- run -->
```lisp
(define (vowel? c) (and (member (char-downcase c) '(#\a #\e #\i #\o #\u)) #t))
(define sentence "Brote is a small Scheme")
(string-join (reverse (string-split sentence " ")) " ")
(count vowel? (string->list sentence))
(string-map char-upcase "shout")
```

```text
"Scheme small a is Brote"
7
"SHOUT"
```

### Reading text from a string

A string port lets code read a string a piece at a time, and `read` parses the data in it. The end of the text is the value `(eof-object)`, so a loop tests for it with `eof-object?`:

<!-- run -->
```lisp
(define (read-all-lines text)
  (let ((port (open-input-string text)))
    (let loop ((acc '()))
      (let ((line (read-line port)))
        (if (eof-object? line)
            (reverse acc)
            (loop (cons line acc)))))))
(read-all-lines "one\ntwo\nthree")
(read (open-input-string "(a (b c) 42 \"text\")"))
(call-with-output-string (lambda (port) (write 'hello port) (write-char #\space port) (write "world" port)))
```

```text
("one" "two" "three")
(a (b c) 42 "text")
"hello \"world\""
```

## Tests

A file of tests is a Scheme file that says what should be true. `brote test` runs the files and reports what failed, with the place of each failure (the [command page](command.md#running-tests) shows it). The forms below are what the files are written with.

| Form | What it does |
|---|---|
| `(test name body …)` | run `body …` and record that it passed, or the error that stopped it. A failing test does not stop the ones after it |
| `(assert expr)` `(assert expr message)` | an error if `expr` is false: `assertion failed: ` and the expression, or `message` and the expression |
| `(assert-equal expected expr)` | an error unless `expr` is `equal?` to `expected`; it shows the expression, what was expected and what came out |
| `(assert-error expr)` `(assert-error expr text)` | an error unless evaluating `expr` raises one; with `text`, one whose message mentions it |
| `test-results` | a procedure of no arguments, `(test-results)`: what the tests so far did, oldest first, as a list of `(name passed? message where trace)` |
| `(run-test name thunk)` | what `test` does, for a procedure of no arguments |

These are for tests: `assert` and the others raise an ordinary error when they fail, so they work anywhere, and `test` is the part that catches it:

<!-- run -->
```lisp
(define (square x) (* x x))
(test "squares" (assert-equal 9 (square 3)))
(test "negatives" (assert-equal 9 (square -3)))
(test "off by one" (assert-equal 10 (square 3)))
(test "bad input" (assert-error (square "x")))
(map (lambda (r) (list (car r) (cadr r))) (test-results))
```

```text
(("squares" #t) ("negatives" #t) ("off by one" #f) ("bad input" #t))
```

## Vectors

A vector is an indexed array you can change in place. Brote has no `make-vector`, so you build one with `vector` or `list->vector`.

| Procedure | What it gives back |
|---|---|
| `(vector x …)` `(vector? x)` | build one / ask |
| `(vector-length v)` `(vector-ref v i)` | the size / the element at index `i` |
| `(vector-set! v i x)` | replace element `i`, which changes the vector |
| `(vector->list v)` `(list->vector l)` | convert |
| `(vector-map f v)` | a new vector with `f` applied to each element |
| `(vector-for-each f v)` | call `f` on each element, for what it does |

<!-- run -->
```lisp
(define v (vector 1 2 3))
(vector-set! v 0 10)
(vector-map (lambda (n) (* n n)) v)
v
```

```text
#(100 4 9)
#(10 2 3)
```

## Hash tables

An association list is fine for a handful of entries, and it's what `json-parse` gives you. Once you have a few hundred entries, or you keep updating the same keys, a hash table is the better tool.

| Procedure | What it does |
|---|---|
| `(make-hash)` | a new, empty table |
| `(hash-set! h key v)` | store `v` under `key`, replacing what was there |
| `(hash-ref h key [default])` | the value under `key`. If it's missing you get `default` when you passed one, and an error otherwise |
| `(hash-has? h key)` | whether `key` has an entry |
| `(hash-remove! h key)` | remove the entry. Removing a key that isn't there is fine |
| `(hash-update! h key proc [default])` | replace the value with `(proc value)`. `default` stands in for a missing value |
| `(hash-count h)` | how many entries there are |
| `(hash-keys h)` `(hash-values h)` `(hash->alist h)` | the contents as new lists |
| `(alist->hash al)` | a table built from an association list |
| `(hash? x)` | whether `x` is a table |

Counting words is the usual first example. `hash-update!` takes the old value (or the default, `0`, the first time) and stores whatever the procedure returns:

<!-- run -->
```lisp
(define counts (make-hash))
(for-each (lambda (w) (hash-update! counts w (lambda (n) (+ n 1)) 0))
          (string-split "the cat saw the other cat and the dog" " "))
(hash-ref counts "the")
(hash-ref counts "bird" 0)
(hash-count counts)
```

```text
3
0
6
```

A missing key without a default is an error, and the message names the key:

<!-- run -->
```lisp
(hash-ref counts "bird")
```

```text
error: hash-ref: no key "bird"
```

### Keys and order

A key can be a string, a symbol, an integer or a boolean, and two keys are the same when `equal?` says so. So the string `"a"` and the symbol `a` are two different keys, and so are `1` and `#t`. Floats, lists and vectors can't be keys. Each would need a rule of its own (is `1.0` the key `1`? does a list mean its contents or its address?), so using one is an error that names its type.

<!-- run -->
```lisp
(define h (make-hash))
(hash-set! h "a" 1)
(hash-set! h 'a 2)
(hash-count h)
(hash-set! h 1.5 3)
```

```text
2
error: hash-set!: argument 2: a hash key is a string, symbol, integer or boolean, got float 1.5
```

The keys, the values and `hash->alist` all come back in the order the keys were first added. Setting a key that's already there leaves it where it was. Removing a key and adding it again moves it to the end.

<!-- run -->
```lisp
(define order (make-hash))
(hash-set! order "b" 1)
(hash-set! order "a" 2)
(hash-set! order "c" 3)
(hash-set! order "b" 10)
(hash-keys order)
(hash-remove! order "a")
(hash-set! order "a" 4)
(hash->alist order)
```

```text
("b" "a" "c")
(("b" . 10) ("c" . 3) ("a" . 4))
```

### Sharing, printing and JSON

A table is shared by everyone who holds it, the way a vector is, so `(define g h)` gives you a second name for the one table, and there's still only that one. For the same reason `equal?` on two tables asks whether they're the same table:

<!-- run -->
```lisp
(define g h)
(hash-set! g "z" 26)
(hash-has? h "z")
(equal? (make-hash) (make-hash))
(equal? g h)
```

```text
#t
#f
#t
```

Printing a table shows the entries. You can't read that text back in, since there's no literal syntax for a table. A table that contains itself prints `#<hash ...>` at the inner level instead of going on forever.

<!-- run -->
```lisp
(define loop (make-hash))
(hash-set! loop "me" loop)
(hash-set! loop "n" 1)
loop
```

```text
#<hash ("me" . #<hash ...>) ("n" . 1)>
```

`json-emit` writes a table as an object, as long as every key is a string. `json-parse` gives back an association list, and `alist->hash` turns that into a table. For a key that appears twice in the list, the later value wins and the key keeps its first place.

<!-- run -->
```lisp
(define doc (alist->hash '(("name" . "brote") ("tags" . ("scheme" "go")))))
(json-emit doc)
(hash-ref (alist->hash (json-parse "{\"a\": 1, \"b\": 2}")) "b")
(hash->alist (alist->hash '(("x" . 1) ("y" . 2) ("x" . 3))))
(json-emit (alist->hash '((1 . "one"))))
```

```text
"{\"name\":\"brote\",\"tags\":[\"scheme\",\"go\"]}"
2
(("x" . 3) ("y" . 2))
error: json-emit: cannot emit a hash with the key 1: JSON object keys are strings
```

### Provenance

If you're embedding Brote and tagging values by where they came from, hash tables carry those tags the same way lists and vectors do. A value you read out of a table keeps its own tags, and anything that makes a string out of a table (`json-emit`, `string-join` on its values, `display`) picks up the tags of everything in it, keys included. The [language reference](scheme.md) has the details under Provenance.
