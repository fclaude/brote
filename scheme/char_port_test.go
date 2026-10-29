package scheme

import (
	"context"
	"strings"
	"testing"
)

func evalAll(t *testing.T, in *Interp, src string) string {
	t.Helper()
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return Write(v)
}

func TestCharacters(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		// the reader and the writer
		{`#\a`, `#\a`},
		{`#\A`, `#\A`},
		{`#\space`, `#\space`},
		{`#\newline`, `#\newline`},
		{`#\tab`, `#\tab`},
		{`#\x41`, `#\A`},
		{`#\x3bb`, `#\λ`},
		{`#\λ`, `#\λ`},
		{`#\(`, `#\(`},
		{`#\)`, `#\)`},
		{`#\"`, `#\"`},
		{`#\;`, `#\;`},
		{`#\x`, `#\x`},
		{`#\nul`, `#\null`},
		{`(list #\a #\b)`, `(#\a #\b)`},
		{`'(#\a . #\b)`, `(#\a . #\b)`},
		{`(integer->char 7)`, `#\alarm`},
		{`(integer->char 1)`, `#\x1`},
		// conversions
		{`(char->integer #\A)`, `65`},
		{`(integer->char 955)`, `#\λ`},
		{`(char-upcase #\a)`, `#\A`},
		{`(char-downcase #\Λ)`, `#\λ`},
		// predicates
		{`(list (char? #\a) (char? "a") (char? 97))`, `(#t #f #f)`},
		{`(list (char-alphabetic? #\a) (char-alphabetic? #\1) (char-alphabetic? #\é))`, `(#t #f #t)`},
		{`(list (char-numeric? #\7) (char-numeric? #\a))`, `(#t #f)`},
		{`(list (char-whitespace? #\space) (char-whitespace? #\newline) (char-whitespace? #\a))`, `(#t #t #f)`},
		{`(list (char-upper-case? #\A) (char-upper-case? #\a) (char-lower-case? #\a))`, `(#t #f #t)`},
		{`(list (digit-value #\7) (digit-value #\a) (digit-value #\٣))`, `(7 #f 3)`},
		// comparison
		{`(list (char=? #\a #\a) (char=? #\a #\b) (char<? #\a #\b #\c) (char>? #\b #\a) (char<=? #\a #\a) (char>=? #\a #\b) (char-ci=? #\a #\A))`, `(#t #f #t #t #t #f #t)`},
		{`(list (eq? #\a #\a) (equal? #\a #\a) (equal? #\a "a") (eqv? #\a #\b))`, `(#t #t #f #f)`},
		// display shows the character itself
		{`(with-output-to-string (display #\a) (display (list #\b "c")) (write #\d))`, `"a(b c)#\\d"`},
		// as hash keys
		{`(let ((h (make-hash))) (hash-set! h #\a 1) (list (hash-ref h #\a) (hash-ref h #\b 0)))`, `(1 0)`},
		// case
		{`(case #\b ((#\a) 'a) ((#\b #\c) 'bc) (else 'other))`, `bc`},
	} {
		if got := evalAll(t, in, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
}

func TestCharacterErrors(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		{`#\bogus`, "unknown character name"},
		{`#\`, "incomplete"},
		{`(char->integer "a")`, "character"},
		{`(integer->char -1)`, "code point"},
		{`(integer->char 55296)`, "code point"}, // a surrogate
		{`(char-upcase 5)`, "character"},
		{`(char<? #\a 1)`, "character"},
	} {
		_, err := in.EvalString(context.Background(), c.src)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), c.want) {
			t.Errorf("%s: error = %v, want one that mentions %q", c.src, err, c.want)
		}
	}
}

func TestCharacterWriteRoundTrips(t *testing.T) {
	for _, r := range []rune{'a', ' ', '\n', '\t', 0, 1, 127, '(', ')', '"', '\\', 'λ', '😀', 0x200b, 0xa0} {
		text := Write(Char(r))
		vs, err := Read(text)
		if err != nil || len(vs) != 1 || vs[0].Kind != KChar || rune(vs[0].I) != r {
			t.Errorf("%U wrote %q, which reads as %v (%v)", r, text, vs, err)
		}
	}
}

func TestStringsOfCharacters(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		{`(string-ref "héllo" 1)`, `#\é`},
		{`(string->list "abc")`, `(#\a #\b #\c)`},
		{`(string->list "abcd" 1 3)`, `(#\b #\c)`},
		{`(list->string (list #\a #\b))`, `"ab"`},
		{`(list->string '())`, `""`},
		{`(string #\a #\b #\c)`, `"abc"`},
		{`(string)`, `""`},
		{`(make-string 3 #\x)`, `"xxx"`},
		{`(make-string 2)`, `"  "`},
		{`(string-copy "hello" 1 3)`, `"el"`},
		{`(string-copy "hello")`, `"hello"`},
		{`(let ((acc '())) (string-for-each (lambda (c) (set! acc (cons c acc))) "abc") acc)`, `(#\c #\b #\a)`},
		{`(string-map char-upcase "abc")`, `"ABC"`},
		{`(list->string (reverse (string->list "stressed")))`, `"desserts"`},
		{`(list (string>? "b" "a") (string<=? "a" "a" "b") (string>=? "a" "b") (string-ci=? "AbC" "aBc"))`, `(#t #t #f #t)`},
		{`(string-length (list->string (map integer->char '(955 955 955))))`, `3`},
	} {
		if got := evalAll(t, in, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
	for _, c := range []struct{ src, want string }{
		{`(string-ref "abc" 3)`, "out of range"},
		{`(string-ref "abc" -1)`, "out of range"},
		{`(string-copy "abc" 2 1)`, "out of bounds"},
		{`(list->string '(1 2))`, "not a character"},
		{`(string-map (lambda (c) 1) "ab")`, "not a character"},
	} {
		_, err := in.EvalString(context.Background(), c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one that mentions %q", c.src, err, c.want)
		}
	}
}

func TestInputStringPorts(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		{`(let ((p (open-input-string "ab"))) (list (read-char p) (peek-char p) (read-char p) (eof-object? (read-char p)) (eof-object? (peek-char p))))`, `(#\a #\b #\b #t #t)`},
		{`(let ((p (open-input-string "one\ntwo\r\n\nlast"))) (list (read-line p) (read-line p) (read-line p) (read-line p) (eof-object? (read-line p))))`, `("one" "two" "" "last" #t)`},
		{`(let ((p (open-input-string "a\n"))) (list (read-line p) (eof-object? (read-line p))))`, `("a" #t)`},
		{`(let ((p (open-input-string "héllo"))) (list (read-string 2 p) (read-string 10 p) (eof-object? (read-string 1 p))))`, `("hé" "llo" #t)`},
		{`(let ((p (open-input-string "(1 \"two\" three) 4 ; c\n 'q"))) (list (read p) (read p) (read p) (eof-object? (read p))))`, `((1 "two" three) 4 (quote q) #t)`},
		{`(let ((p (open-input-string "12 abc"))) (list (read p) (read p) (eof-object? (read p))))`, `(12 abc #t)`},
		{`(let ((p (open-input-string "#\\a #\\space"))) (list (read p) (read p)))`, `(#\a #\space)`},
		{`(let ((p (open-input-string "x y"))) (list (read-char p) (read p) (eof-object? (read p))))`, `(#\x y #t)`},
		{`(eof-object? (read (open-input-string "   ; nothing\n")))`, `#t`},
		{`(list (port? (open-input-string "")) (input-port? (open-input-string "")) (output-port? (open-input-string "")))`, `(#t #t #f)`},
		{`(eof-object? (eof-object))`, `#t`},
		{`(eof-object? "")`, `#f`},
		{`(let ((p (open-input-string "abc"))) (close-port p) (eof-object? (read-char p)))`, `#t`},
		// the end of the current input, which has nothing in it by default
		{`(eof-object? (read-line))`, `#t`},
		{`(eof-object? (read-char (current-input-port)))`, `#t`},
	} {
		if got := evalAll(t, in, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
	for _, c := range []struct{ src, want string }{
		{`(read (open-input-string "(1 2"))`, "end of input"},
		{`(read (open-input-string ")"))`, "unexpected"},
		{`(read-char (open-output-string))`, "input port"},
		{`(read-char 5)`, "port"},
		{`(read-string -1 (open-input-string "a"))`, "count"},
	} {
		_, err := in.EvalString(context.Background(), c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one that mentions %q", c.src, err, c.want)
		}
	}
}

func TestOutputStringPorts(t *testing.T) {
	var out strings.Builder
	in := New(WithOutput(&out))
	for _, c := range []struct{ src, want string }{
		{`(let ((p (open-output-string))) (display "a" p) (write "b" p) (write-string "c" p) (write-char #\d p) (newline p) (display 1.5 p) (get-output-string p))`, `"a\"b\"cd\n1.5"`},
		{`(call-with-output-string (lambda (p) (display (list 1 "x") p) (write (list 1 "x") p)))`, `"(1 x)(1 \"x\")"`},
		{`(let ((p (open-output-string))) (get-output-string p))`, `""`},
		{`(let ((p (open-output-string))) (display "x" p) (display "y" p) (list (get-output-string p) (get-output-string p)))`, `("xy" "xy")`},
		{`(list (output-port? (open-output-string)) (output-port? (current-output-port)) (input-port? (current-output-port)))`, `(#t #t #f)`},
		{`(with-output-to-string (display "in") (display "!" (current-output-port)))`, `"in!"`},
		{`(let ((p (open-output-string))) (display "kept" p) (with-output-to-string (display "captured")))`, `"captured"`},
		{`(let ((p (open-output-string))) (close-port p) (guard (e (#t 'closed)) (display "x" p)))`, `closed`},
		{`(begin (display "to stdout" (current-output-port)) (write-string "!" (current-output-port)) (write-char #\? (current-output-port)) (newline (current-output-port)))`, `#<void>`},
	} {
		if got := evalAll(t, in, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.src, got, c.want)
		}
	}
	if got, want := out.String(), "to stdout!?\n"; got != want {
		t.Errorf("standard output = %q, want %q", got, want)
	}
	for _, c := range []struct{ src, want string }{
		{`(display "x" (open-input-string ""))`, "output port"},
		{`(get-output-string (open-input-string ""))`, "output port"},
		{`(get-output-string (current-output-port))`, "output port"},
		{`(write-string #\a)`, "string"},
		{`(write-char "a")`, "character"},
	} {
		_, err := in.EvalString(context.Background(), c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one that mentions %q", c.src, err, c.want)
		}
	}
}

func TestWithInputIsTheCurrentInput(t *testing.T) {
	in := New(WithInput(strings.NewReader("first\nsecond (1 2)\nthird")))
	if got := evalAll(t, in, `(list (read-line) (read-char) (read-string 5) (read) (read-char) (read-line) (eof-object? (read-line)))`); got != `("first" #\s "econd" (1 2) #\newline "third" #t)` {
		t.Errorf("got %s", got)
	}
}

func TestStringPortKeepsTheTaintOfItsText(t *testing.T) {
	in := New()
	in.mustRegister("tainted-text", "Text with a provenance.", nil, false, func(c *Call) (Value, error) {
		return Tainted(Str("secret (1 2)"), TaintOf("test:source")), nil
	})
	v, err := in.EvalString(context.Background(), `(let ((p (open-input-string (tainted-text)))) (list (read-string 6 p) (read p)))`)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := v.Slice()
	for i, it := range items {
		if !it.Taint.Has("test:source") {
			t.Errorf("result %d lost its taint: %s", i, Write(it))
		}
	}
	// Text written to a port carries the taint of what was written.
	v, err = in.EvalString(context.Background(), `(let ((p (open-output-string))) (display (tainted-text) p) (get-output-string p))`)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Taint.Has("test:source") {
		t.Errorf("get-output-string lost the taint: %s", v.Taint)
	}
}

func TestPortsAreSafeForTasks(t *testing.T) {
	in := New(WithTasks(TaskLimits{}))
	got := evalAll(t, in, `
(define p (open-output-string))
(define ts (map (lambda (i) (spawn (lambda () (let loop ((n 0)) (when (< n 200) (write-char #\x p) (loop (+ n 1))))))) (iota 8)))
(for-each task-wait ts)
(string-length (get-output-string p))`)
	if got != "1600" {
		t.Errorf("got %s, want 1600", got)
	}
}
