package scheme

import (
	"context"
	"strings"
	"testing"
)

func TestSyntaxRules(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"swap with a hidden temporary", `
(define-syntax swap!
  (syntax-rules ()
    ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))
(define tmp 1) (define y 2)
(swap! tmp y)
(list tmp y)`, `(2 1)`},
		{"unless-like with a literal", `
(define-syntax my-if
  (syntax-rules (then else)
    ((_ c then a else b) (cond (c a) (else b)))))
(list (my-if #t then 1 else 2) (my-if #f then 1 else 2))`, `(1 2)`},
		{"recursive: or", `
(define-syntax my-or
  (syntax-rules ()
    ((_) #f)
    ((_ e) e)
    ((_ e r ...) (let ((t e)) (if t t (my-or r ...))))))
(define t 5)
(list (my-or) (my-or #f #f 3) (my-or #f t))`, `(#f 3 5)`},
		{"while loop", `
(define-syntax while
  (syntax-rules ()
    ((_ c body ...) (let loop () (when c body ... (loop))))))
(define i 0) (define acc '())
(while (< i 4) (set! acc (cons i acc)) (set! i (+ i 1)))
acc`, `(3 2 1 0)`},
		{"ellipsis in the middle of a pattern", `
(define-syntax last-of
  (syntax-rules ()
    ((_ a ... z) 'z)))
(list (last-of 1 2 3) (last-of 9))`, `(3 9)`},
		{"nested ellipsis", `
(define-syntax my-let*
  (syntax-rules ()
    ((_ () body ...) (let () body ...))
    ((_ ((n v) rest ...) body ...) (let ((n v)) (my-let* (rest ...) body ...)))))
(my-let* ((a 1) (b (+ a 1)) (c (* b 2))) (list a b c))`, `(1 2 4)`},
		{"nested ellipsis depth two", `
(define-syntax flat
  (syntax-rules ()
    ((_ (a ...) ...) '(a ... ...))))
(flat (1 2) (3) () (4 5))`, `(1 2 3 4 5)`},
		{"keeps the shape of groups", `
(define-syntax groups
  (syntax-rules ()
    ((_ (k v ...) ...) '((k (v ...)) ...))))
(groups (a 1 2) (b) (c 3))`, `((a (1 2)) (b ()) (c (3)))`},
		{"my own cond", `
(define-syntax my-cond
  (syntax-rules (else)
    ((_ (else e ...)) (begin e ...))
    ((_ (c e ...) clause ...) (if c (begin e ...) (my-cond clause ...)))))
(my-cond (#f 1) ((= 1 2) 2) (else 3))`, `3`},
		{"a dotted pattern", `
(define-syntax f
  (syntax-rules ()
    ((_ a . rest) '(a rest))))
(f 1 2 3)`, `(1 (2 3))`},
		{"underscore ignores", `
(define-syntax second-of
  (syntax-rules ()
    ((_ _ b _) b)))
(second-of 1 2 3)`, `2`},
		{"vector pattern and template", `
(define-syntax vswap
  (syntax-rules ()
    ((_ #(a b)) #(b a))))
(vswap #(1 2))`, `#(2 1)`},
		{"a custom ellipsis", `
(define-syntax tail-of
  (syntax-rules ::: ()
    ((_ a b :::) '(b :::))))
(tail-of 1 2 3)`, `(2 3)`},
		{"an escaped ellipsis", `
(define-syntax def-dots
  (syntax-rules ()
    ((_ name) (define-syntax name (syntax-rules () ((_ x (... ...)) '(x (... ...))))))))
(def-dots collect)
(collect 1 2 3)`, `(1 2 3)`},
		{"define through a macro", `
(define-syntax def
  (syntax-rules () ((_ n v) (define n v))))
(def answer 42)
answer`, `42`},
		{"a macro in a body is local", `
(define (f x)
  (define-syntax double (syntax-rules () ((_ e) (* 2 e))))
  (double x))
(list (f 4) (guard (e (#t 'unbound)) (double 1)))`, `(8 unbound)`},
		{"let-syntax", `
(let-syntax ((inc (syntax-rules () ((_ x) (+ x 1)))))
  (inc 41))`, `42`},
		{"a variable hides a macro", `
(define-syntax m (syntax-rules () ((_ x) (list x))))
(define (f m) (m 3))
(f (lambda (x) (* x x)))`, `9`},
		{"expanding again in a loop", `
(define-syntax add1 (syntax-rules () ((_ x) (+ x 1))))
(define (count n) (let loop ((i 0) (a 0)) (if (= i n) a (loop (+ i 1) (add1 a)))))
(count 1000)`, `1000`},
		{"a macro is found when the form runs, like a procedure", `
(define-syntax m (syntax-rules () ((_) 1)))
(define (f) (m))
(define a (f))
(define-syntax m (syntax-rules () ((_) 2)))
(list a (f))`, `(1 2)`},
		{"the template's quote keeps names", `
(define-syntax q (syntax-rules () ((_ a) (let ((x a)) (list 'x x)))))
(q 5)`, `(x 5)`},
		{"a macro generating a macro", `
(define-syntax def-const
  (syntax-rules ()
    ((_ name val) (define-syntax name (syntax-rules () ((_) val))))))
(def-const seven 7)
(seven)`, `7`},
		{"used as a value is not a procedure", `
(define-syntax m (syntax-rules () ((_) 1)))
(list (procedure? 1) (guard (e (#t 'error)) (map m '(1 2))))`, `(#f error)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := New().EvalString(context.Background(), c.src)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got := Write(v); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestSyntaxRulesErrors(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"no rule fits", `(define-syntax m (syntax-rules () ((_ a) a))) (m 1 2)`, "no pattern matches"},
		{"not syntax-rules", `(define-syntax m (lambda (x) x))`, "syntax-rules"},
		{"a special form", `(define-syntax if (syntax-rules () ((_) 1)))`, "special form"},
		{"a name twice in a pattern", `(define-syntax m (syntax-rules () ((_ a a) a)))`, "twice"},
		{"an ellipsis without one in the pattern", `(define-syntax m (syntax-rules () ((_ a) (a ...)))) (m 1)`, "ellipsis"},
		{"a variable under ellipsis used without it", `(define-syntax m (syntax-rules () ((_ a ...) a))) (m 1)`, "ellipsis"},
		{"syntax-rules by itself", `(syntax-rules () ((_) 1))`, "define-syntax"},
		{"a malformed rule", `(define-syntax m (syntax-rules () (_ 1)))`, "pattern"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := New().EvalString(context.Background(), c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want one that mentions %q", err, c.want)
			}
		})
	}
}

func TestMacroUseErrorsPointAtTheUse(t *testing.T) {
	se := evalNamedErr(t, "m.scm", "(define-syntax m (syntax-rules () ((_ a) (car a))))\n\n  (m 5)")
	if got := se.Pos.String(); got != "m.scm:1:44" && got != "m.scm:3:3" {
		t.Errorf("Pos = %s", got)
	}
}

func TestMacrosAreShownByDoc(t *testing.T) {
	in := New()
	if _, err := in.EvalString(context.Background(), `(define-syntax m (syntax-rules () ((_) 1)))`); err != nil {
		t.Fatal(err)
	}
	if v, _ := in.EvalString(context.Background(), `(doc 'define-syntax)`); !strings.Contains(Write(v), "syntax-rules") {
		t.Errorf("no doc for define-syntax: %s", Write(v))
	}
}

func TestMacroExpansionIsSafeForTasks(t *testing.T) {
	in := New(WithTasks(TaskLimits{}))
	v, err := in.EvalString(context.Background(), `
(define-syntax add1 (syntax-rules () ((_ x) (let ((t x)) (+ t 1)))))
(define (work n) (let loop ((i 0) (a 0)) (if (= i n) a (loop (+ i 1) (add1 a)))))
(map task-wait (map (lambda (i) (spawn (lambda () (work 500)))) (iota 8)))`)
	if err != nil {
		t.Fatal(err)
	}
	if got := Write(v); got != "(500 500 500 500 500 500 500 500)" {
		t.Errorf("got %s", got)
	}
}

func TestDocOfAMacroShowsItsPatterns(t *testing.T) {
	in := New()
	v, err := in.EvalString(context.Background(), `(define-syntax my-or (syntax-rules () ((_) #f) ((_ e r ...) (if e e (my-or r ...))))) (doc 'my-or)`)
	if err != nil {
		t.Fatal(err)
	}
	if got := Write(v); got != `"(my-or)\n(my-or e r ...)\nA macro."` {
		t.Errorf("got %s", got)
	}
}

func TestRunawayExpansionIsStopped(t *testing.T) {
	_, err := New().EvalString(context.Background(), `
(define-syntax grow (syntax-rules () ((_ x ...) (grow x ... x ...))))
(grow 1 2)`)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("error = %v, want one about the size of the expansion", err)
	}
}

func TestRunawayExpansionStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100_000_000) // 0.1s
	defer cancel()
	_, err := New().EvalString(ctx, `
(define-syntax spin (syntax-rules () ((_) (spin))))
(spin)`)
	if err == nil {
		t.Fatal("want the evaluation to be stopped")
	}
}
