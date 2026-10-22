package scheme

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHashTables(t *testing.T) {
	cases := []struct{ src, want string }{
		{`(hash? (make-hash))`, `#t`}, {`(hash? '())`, `#f`}, {`(hash? (vector))`, `#f`},
		{`(hash-count (make-hash))`, `0`},
		{`(define h (make-hash)) (hash-set! h "a" 1) (hash-ref h "a")`, `1`},
		{`(define h (make-hash)) (hash-set! h 'a 1) (hash-ref h 'a)`, `1`},
		{`(define h (make-hash)) (hash-set! h 7 'seven) (hash-ref h 7)`, `seven`},
		{`(define h (make-hash)) (hash-set! h #t "yes") (hash-ref h #t)`, `"yes"`},
		// A string and a symbol with the same letters are different keys.
		{`(define h (make-hash)) (hash-set! h "a" 1) (hash-has? h 'a)`, `#f`},
		{`(define h (make-hash)) (hash-set! h 1 'one) (hash-has? h #t)`, `#f`},
		{`(define h (make-hash)) (hash-ref h "x" 'none)`, `none`},
		{`(define h (make-hash)) (hash-set! h "a" 1) (hash-set! h "a" 2) (list (hash-ref h "a") (hash-count h))`, `(2 1)`},
		{`(define h (make-hash)) (hash-set! h "a" 1) (hash-remove! h "a") (list (hash-has? h "a") (hash-count h))`, `(#f 0)`},
		{`(define h (make-hash)) (hash-remove! h "never") (hash-count h)`, `0`},
		{`(define h (make-hash)) (hash-set! h "b" 1) (hash-set! h "a" 2) (hash-set! h 3 3) (hash-keys h)`, `("b" "a" 3)`},
		{`(define h (make-hash)) (hash-set! h "b" 1) (hash-set! h "a" 2) (hash-values h)`, `(1 2)`},
		{`(define h (make-hash)) (hash-set! h "b" 1) (hash-set! h "a" 2) (hash->alist h)`, `(("b" . 1) ("a" . 2))`},
		// Setting a key again keeps its place.
		{`(define h (make-hash)) (hash-set! h "b" 1) (hash-set! h "a" 2) (hash-set! h "b" 9) (hash->alist h)`, `(("b" . 9) ("a" . 2))`},
		// Removing and adding puts the key at the end.
		{`(define h (make-hash)) (hash-set! h "b" 1) (hash-set! h "a" 2) (hash-remove! h "b") (hash-set! h "b" 3) (hash-keys h)`, `("a" "b")`},
		{`(hash->alist (alist->hash '(("x" . 1) ("y" . 2))))`, `(("x" . 1) ("y" . 2))`},
		{`(hash->alist (alist->hash '(("x" . 1) ("y" . 2) ("x" . 3))))`, `(("x" . 3) ("y" . 2))`},
		{`(hash-count (alist->hash '()))`, `0`},
		{`(define h (make-hash)) (hash-update! h "n" (lambda (v) (+ v 1)) 0) (hash-update! h "n" (lambda (v) (+ v 1)) 0) (hash-ref h "n")`, `2`},
		{`(define h (make-hash)) (hash-set! h "n" 5) (hash-update! h "n" (lambda (v) (* v 2))) (hash-ref h "n")`, `10`},
		// Values can be anything, including tables and procedures.
		{`(define h (make-hash)) (define g (make-hash)) (hash-set! h "g" g) (hash-set! g "x" 1) (hash-ref (hash-ref h "g") "x")`, `1`},
		{`(define h (make-hash)) (hash-set! h "f" car) ((hash-ref h "f") '(1 2))`, `1`},
		// A table is shared by whoever holds it.
		{`(define h (make-hash)) (define g h) (hash-set! g "a" 1) (hash-has? h "a")`, `#t`},
		// equal? on tables is identity.
		{`(equal? (make-hash) (make-hash))`, `#f`}, {`(define h (make-hash)) (equal? h h)`, `#t`}, {`(eq? (make-hash) (make-hash))`, `#f`},
		// Printing.
		{`(make-hash)`, `#<hash>`},
		{`(define h (make-hash)) (hash-set! h "a" 1) (hash-set! h 'b "x") h`, `#<hash ("a" . 1) (b . "x")>`},
		{`(define h (make-hash)) (hash-set! h "a" "x") (with-output-to-string (display h))`, `"#<hash (a . x)>"`},
		{`(define h (make-hash)) (hash-set! h "self" h) h`, `#<hash ("self" . #<hash ...>)>`},
		{`(json-emit (alist->hash '(("a" . 1) ("b" . (1 2)))))`, `"{\"a\":1,\"b\":[1,2]}"`},
		{`(json-emit (make-hash))`, `"{}"`},
		{`(json-emit (alist->hash (list (cons "n" (alist->hash '(("deep" . #t)))))))`, `"{\"n\":{\"deep\":true}}"`},
		{`(json-parse (json-emit (alist->hash '(("a" . 1)))))`, `(("a" . 1))`},
		{`(procedure? hash-ref)`, `#t`},
	}
	for _, c := range cases {
		got, err := New().EvalString(context.Background(), c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if Write(got) != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, Write(got), c.want)
		}
	}
}

func TestHashErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`(hash-ref (make-hash) "k")`, `hash-ref: no key "k"`},
		{`(hash-ref (make-hash) 'k)`, `hash-ref: no key k`},
		{`(hash-update! (make-hash) "k" car)`, `hash-update!: no key "k"`},
		{`(hash-set! (make-hash) 1.5 1)`, `hash-set!: argument 2: a hash key is a string, symbol, integer or boolean, got float 1.5`},
		{`(hash-set! (make-hash) '(1) 1)`, `got pair (1)`},
		{`(hash-ref (make-hash) (vector 1))`, `got vector #(1)`},
		{`(hash-has? (make-hash) '())`, `got () ()`},
		{`(hash-ref '(("a" . 1)) "a")`, `hash-ref: argument 1: expected hash, got pair`},
		{`(hash-count 5)`, `hash-count: argument 1: expected hash, got integer 5`},
		{`(hash-keys "x")`, `expected hash`},
		{`(alist->hash '(1 2))`, `alist->hash: element 1 is 1, not a pair`},
		{`(alist->hash '((1.5 . 2)))`, `alist->hash: element 1: a hash key is`},
		{`(alist->hash 5)`, `alist->hash`},
		{`(hash-update! (make-hash) "k" 5 0)`, `expected procedure`},
		{`(hash-ref (make-hash))`, `hash-ref: expected 2 to 3 argument(s), got 1`},
		{`(json-emit (alist->hash '((1 . 2))))`, `json-emit: cannot emit a hash with the key 1: JSON object keys are strings`},
		{`(json-emit (alist->hash '((k . 2))))`, `JSON object keys are strings`},
	}
	for _, c := range cases {
		_, err := New().EvalString(context.Background(), c.src)
		if err == nil {
			t.Errorf("%s: accepted", c.src)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s:\n got %v\nwant it to contain %q", c.src, err, c.want)
		}
	}
	// A failed update leaves the entry as it was.
	in := New()
	eval(t, in, `(define h (make-hash)) (hash-set! h "n" 1)`)
	if _, err := in.EvalString(context.Background(), `(hash-update! h "n" (lambda (v) (error "boom")))`); err == nil {
		t.Fatal("the error in the procedure was swallowed")
	}
	if got := Write(eval(t, in, `(hash-ref h "n")`)); got != "1" {
		t.Fatalf("after a failed update the value is %s", got)
	}
}

// The order survives many removals, which squeeze the holes out.
func TestHashOrderSurvivesRemovalAndCompaction(t *testing.T) {
	in := New()
	eval(t, in, `(define h (make-hash))
		(define (fill i) (if (< i 200) (begin (hash-set! h i (* i i)) (fill (+ i 1)))))
		(fill 0)
		(define (drop-odd i) (if (< i 200) (begin (if (odd? i) (hash-remove! h i)) (drop-odd (+ i 1)))))
		(drop-odd 0)`)
	keys := eval(t, in, `(hash-keys h)`)
	items, _ := keys.Slice()
	if len(items) != 100 {
		t.Fatalf("%d keys left, want 100", len(items))
	}
	for i, k := range items {
		if k.I != int64(2*i) {
			t.Fatalf("key %d is %d, want %d", i, k.I, 2*i)
		}
	}
	if got := Write(eval(t, in, `(hash-ref h 40)`)); got != "1600" {
		t.Fatalf("after compaction (hash-ref h 40) is %s", got)
	}
	eval(t, in, `(hash-set! h 1 'back)`)
	last := eval(t, in, `(car (list-tail (hash-keys h) 100))`)
	if last.I != 1 {
		t.Fatalf("a re-added key goes to the end, got %s", Write(last))
	}
}

// A random sequence of operations against a Go map and an order slice.
func TestHashAgainstAModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 20; round++ {
		h := NewHash()
		model := map[hashKey]int64{}
		var order []hashKey
		key := func() Value {
			switch rng.Intn(4) {
			case 0:
				return Str(fmt.Sprint("s", rng.Intn(12)))
			case 1:
				return Sym(fmt.Sprint("s", rng.Intn(12)))
			case 2:
				return Int(int64(rng.Intn(12)))
			}
			return Bool(rng.Intn(2) == 0)
		}
		for step := 0; step < 400; step++ {
			k := key()
			hk, _ := keyOf(k)
			switch rng.Intn(3) {
			case 0, 1:
				v := rng.Int63n(1000)
				h.Hash.set(hk, k, Int(v))
				if _, ok := model[hk]; !ok {
					order = append(order, hk)
				}
				model[hk] = v
			case 2:
				h.Hash.remove(hk)
				if _, ok := model[hk]; ok {
					delete(model, hk)
					for i, o := range order {
						if o == hk {
							order = append(order[:i], order[i+1:]...)
							break
						}
					}
				}
			}
			if h.Hash.live != len(model) {
				t.Fatalf("round %d step %d: %d live, model has %d", round, step, h.Hash.live, len(model))
			}
		}
		var got []hashKey
		h.Hash.each(func(k, v Value) {
			hk, _ := keyOf(k)
			got = append(got, hk)
			if model[hk] != v.I {
				t.Fatalf("round %d: value under %v is %d, model has %d", round, hk, v.I, model[hk])
			}
		})
		if fmt.Sprint(got) != fmt.Sprint(order) {
			t.Fatalf("round %d: order %v, model %v", round, got, order)
		}
	}
}

func TestHashTaint(t *testing.T) {
	in := withSource(t)
	eval(t, in, `(define h (make-hash))
		(hash-set! h "dirty" (read-thing "x"))
		(hash-set! h "n" 42)`)

	tainted := func(src string) bool { return tainted(t, in, src).Taint.Has(TagFile) }
	for _, src := range []string{
		`(hash-ref h "dirty")`,
		`(hash-ref h "missing" (read-thing "d"))`,
		`(car (hash-values h))`,
		`(cdr (car (hash->alist h)))`,
		`(string-join (list (car (hash-values h))) ",")`,
		`(json-emit (hash-values h))`,
		`(string-append "x" (hash-ref h "dirty"))`,
		`(json-emit h)`,
		`(with-output-to-string (display h))`,
	} {
		if !tainted(src) {
			t.Errorf("%s: no taint, want %s", src, TagFile)
		}
	}
	// Taint stays on the entries that have it, for results that are not strings
	// (a string made from a container takes everything the container holds, for
	// vectors as for tables).
	for _, src := range []string{
		`(hash-ref h "n")`,
		`(cadr (hash-values h))`,
		`(hash-has? h "n")`,
		`(hash-count h)`,
		`(hash? h)`,
	} {
		if tainted(src) {
			t.Errorf("%s: carries %s, want a clean result", src, TagFile)
		}
	}
	// A table made from a tainted association list is tainted as a whole.
	if !tainted(`(hash-ref (alist->hash (list (cons "a" (read-thing "x")))) "a")`) {
		t.Error("alist->hash of tainted data is clean")
	}
	// A tainted key stays tainted, and writing it over a clean key adds to the key already there.
	eval(t, in, `(define k (make-hash)) (hash-set! k (read-thing "key") 1)
		(define b (make-hash)) (hash-set! b "contents of key" 1) (hash-set! b (read-thing "key") 2)`)
	for _, src := range []string{`(car (hash-keys k))`, `(car (hash-keys b))`, `(caar (hash->alist b))`} {
		if !tainted(src) {
			t.Errorf("%s: the key lost its taint", src)
		}
	}
	if got := Write(eval(t, in, `(hash-count b)`)); got != "1" {
		t.Errorf("the two spellings are two keys: count %s", got)
	}
	// DeepTaint sees keys and values, and ends on a table that holds itself.
	if !DeepTaint(eval(t, in, `h`)).Has(TagFile) || !DeepTaint(eval(t, in, `k`)).Has(TagFile) {
		t.Error("DeepTaint missed what a table holds")
	}
	self := eval(t, in, `(define s (make-hash)) (hash-set! s "me" s) (hash-set! s "x" (read-thing "y")) s`)
	if !DeepTaint(self).Has(TagFile) {
		t.Error("DeepTaint missed a value in a table that holds itself")
	}
	// Redaction by WriteWithStrings reaches keys and values.
	got := WriteWithStrings(eval(t, in, `h`), func(s string) string { return strings.ToUpper(s) })
	if got != `#<hash ("DIRTY" . "CONTENTS OF X") ("N" . 42)>` {
		t.Errorf("WriteWithStrings: %s", got)
	}
}

// A procedure that never returns, run inside hash-update!, stops when the
// context does.
func TestHashUpdateCanBeCancelled(t *testing.T) {
	in := New()
	eval(t, in, `(define h (make-hash))`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := in.EvalString(ctx, `(hash-update! h "k" (lambda (v) (let loop () (loop))) 0)`)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the loop ended without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hash-update! did not stop when its context ended")
	}
	if got := Write(eval(t, in, `(hash-has? h "k")`)); got != "#f" {
		t.Fatalf("a cancelled update stored something: %s", got)
	}
}

// Interpreters share nothing, tables included; the race detector checks the
// "nothing".
func TestHashTablesBelongToTheirInterpreter(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := New()
			v, err := in.EvalString(context.Background(), fmt.Sprintf(`(define h (make-hash))
				(define (go n) (if (< n 300) (begin (hash-set! h n (* n %d)) (go (+ n 1)))))
				(go 0) (hash-count h)`, i))
			if err != nil || v.I != 300 {
				t.Errorf("interpreter %d: %v %v", i, Write(v), err)
			}
			if _, err := in.EvalString(context.Background(), `(hash-ref other "x")`); err == nil {
				t.Errorf("interpreter %d saw a name from another", i)
			}
		}(i)
	}
	wg.Wait()
}

func TestPrelude(t *testing.T) {
	cases := []struct{ src, want string }{
		{`(first '(a b c))`, `a`}, {`(second '(a b c))`, `b`}, {`(third '(a b c))`, `c`}, {`(rest '(a b c))`, `(b c)`},
		{`(take '(1 2 3 4) 2)`, `(1 2)`}, {`(take '(1 2) 0)`, `()`}, {`(take '(1 2) 2)`, `(1 2)`},
		{`(drop '(1 2 3 4) 2)`, `(3 4)`}, {`(drop '(1 2) 2)`, `()`},
		{`(last '(1 2 3))`, `3`}, {`(last '(1))`, `1`},
		{`(any even? '(1 3 4))`, `#t`}, {`(any even? '(1 3))`, `#f`}, {`(any even? '())`, `#f`},
		{`(any (lambda (x) (and (> x 2) (* x 10))) '(1 3 5))`, `30`},
		{`(every number? '(1 2))`, `#t`}, {`(every number? '())`, `#t`}, {`(every even? '(2 3))`, `#f`},
		{`(every (lambda (x) (* x 2)) '(1 2 3))`, `6`},
		{`(count even? '(1 2 4))`, `2`}, {`(count even? '())`, `0`},
		{`(remove even? '(1 2 3 4))`, `(1 3)`}, {`(delete 2 '(1 2 3 2))`, `(1 3)`},
		{`(delete "a" '("a" "b" "a"))`, `("b")`},
		{`(delete-duplicates '(1 2 1 "a" "a" 3 2))`, `(1 2 "a" 3)`}, {`(delete-duplicates '())`, `()`},
		{`(list-index even? '(1 3 4))`, `2`}, {`(list-index even? '(1))`, `#f`},
		{`(partition even? '(1 2 3 4))`, `((2 4) (1 3))`}, {`(partition even? '())`, `(() ())`},
		{`(filter-map (lambda (x) (and (even? x) (* x x))) '(1 2 3 4))`, `(4 16)`},
		{`(append-map (lambda (x) (list x x)) '(1 2))`, `(1 1 2 2)`}, {`(append-map list '())`, `()`},
		{`(string-index "hello world" "o w")`, `4`}, {`(string-index "abc" "z")`, `#f`}, {`(string-index "abc" "")`, `0`}, {`(string-index "" "a")`, `#f`},
		{`(string-pad-left "7" 3 "0")`, `"007"`}, {`(string-pad-left "7" 3)`, `"  7"`}, {`(string-pad-left "abcdef" 3)`, `"abcdef"`},
		{`(string-pad-right "ab" 5)`, `"ab   "`}, {`(string-pad-right "ab" 5 ".")`, `"ab..."`},
		{`(vector-map (lambda (x) (* x x)) #(1 2 3))`, `#(1 4 9)`}, {`(vector-map car #())`, `#()`},
		{`(with-output-to-string (vector-for-each display #(1 2 3)))`, `"123"`},
	}
	for _, c := range cases {
		got, err := New().EvalString(context.Background(), c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if Write(got) != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, Write(got), c.want)
		}
	}
	for _, c := range []struct{ src, want string }{
		{`(take '(1 2) 3)`, `take: the list is shorter than 3`},
		{`(drop '(1 2) 3)`, `drop: the list is shorter than 3`},
		{`(last '())`, `last: the list is empty`},
	} {
		if _, err := New().EvalString(context.Background(), c.src); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.src, err, c.want)
		}
	}
}

// Every procedure the prelude defines, and every hash builtin, is described
// in docs/stdlib.md or the reference.
func TestStdlibIsDocumented(t *testing.T) {
	std, err := os.ReadFile(filepath.Join("..", "docs", "stdlib.md"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := os.ReadFile(filepath.Join("..", "docs", "scheme.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(std) + string(ref)
	def := regexp.MustCompile(`(?m)^\(define \(([^ )]+)`)
	files, _ := filepath.Glob(filepath.Join("prelude", "*.scm"))
	if len(files) == 0 {
		t.Fatal("no prelude files")
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		for _, m := range def.FindAllStringSubmatch(string(src), -1) {
			if !strings.Contains(text, "`"+m[1]+"`") && !strings.Contains(text, "`("+m[1]+" ") {
				t.Errorf("%s: %s is not in docs/stdlib.md or docs/scheme.md", f, m[1])
			}
		}
	}
	for _, p := range New().Prims() {
		if strings.HasPrefix(p.Name, "hash") || strings.HasSuffix(p.Name, "hash") || p.Name == "make-hash" || p.Name == "alist->hash" {
			if !strings.Contains(text, "`("+p.Name+" ") && !strings.Contains(text, "`("+p.Name+")") {
				t.Errorf("%s is not in the docs", p.Name)
			}
		}
	}
}
