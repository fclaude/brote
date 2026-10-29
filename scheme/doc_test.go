package scheme

import (
	"context"
	"strings"
	"testing"
)

func TestDoc(t *testing.T) {
	in := New()
	for _, c := range []struct{ src, want string }{
		{`(doc 'car)`, "(car"},
		{`(doc 'take)`, `"(take l n)\nThe first n elements of l."`},
		{`(doc 'let)`, "(let ((name expr) …) body …)"},
		{`(doc 'no-such-name)`, `#f`},
		{`(doc car)`, "(car"},
		{`(define (twice x) "Double x." (* 2 x)) (doc 'twice)`, `"(twice x)\nDouble x."`},
		{`(define (plain x) x) (doc 'plain)`, `"(plain x)"`},
		{`(define (rest-args a . more) "Takes some." a) (doc 'rest-args)`, `"(rest-args a . more)\nTakes some."`},
		{`(doc (lambda (x) "Anonymous." x))`, `"( x)\nAnonymous."`},
	} {
		v, err := in.EvalString(context.Background(), c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got := Write(v); !strings.Contains(got, c.want) {
			t.Errorf("%s = %s, want it to contain %s", c.src, got, c.want)
		}
	}
}

func TestDocstringIsNotEvaluatedOrKeptInTheBody(t *testing.T) {
	in := New()
	v, err := in.EvalString(context.Background(), `(define (f) "Doc." 1 2) (list (f) ((lambda () "just a string")))`)
	if err != nil {
		t.Fatal(err)
	}
	if got := Write(v); got != `(2 "just a string")` {
		t.Errorf("got %s", got)
	}
}

func TestApropos(t *testing.T) {
	in := New()
	v, err := in.EvalString(context.Background(), `(apropos "vector-m")`)
	if err != nil {
		t.Fatal(err)
	}
	if got := Write(v); got != "(vector-map)" {
		t.Errorf("got %s", got)
	}
	v, _ = in.EvalString(context.Background(), `(apropos "guard")`)
	if got := Write(v); got != "(guard)" {
		t.Errorf("got %s, want the special form", got)
	}
}
