package scheme

import (
	"context"
	"strings"
	"testing"
)

func TestGensym(t *testing.T) {
	in := New()

	// (gensym) and (gensym "prefix") both evaluate to symbols.
	for _, src := range []string{`(symbol? (gensym))`, `(symbol? (gensym "prefix"))`} {
		if got := Write(eval(t, in, src)); got != "#t" {
			t.Errorf("%s => %s, want #t", src, got)
		}
	}

	// Every call yields a name no earlier call produced.
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s := eval(t, in, `(gensym)`)
		if s.Kind != KSymbol {
			t.Fatalf("(gensym) => %s, want a symbol", Write(s))
		}
		if seen[s.S] {
			t.Fatalf("two gensym calls both gave %s", s.S)
		}
		seen[s.S] = true
	}

	// The prefix form's name starts with the prefix and keeps the ~n shape
	// macro expansion uses.
	p := eval(t, in, `(gensym "prefix")`)
	if p.Kind != KSymbol || !strings.HasPrefix(p.S, "prefix~") {
		t.Errorf(`(gensym "prefix") => %s, want a prefix~n name`, Write(p))
	}

	// The result is not eq? to any symbol literal the script can write.
	if got := Write(eval(t, in, `(eq? (gensym "zz") 'zz)`)); got != "#f" {
		t.Errorf(`(eq? (gensym "zz") 'zz) => %s, want #f`, got)
	}
	if got := Write(eval(t, in, `(eq? (gensym) 'gensym)`)); got != "#f" {
		t.Errorf(`(eq? (gensym) 'gensym) => %s, want #f`, got)
	}

	// The argument is a string.
	if _, err := in.EvalString(context.Background(), `(gensym 5)`); err == nil {
		t.Error(`(gensym 5) did not fail`)
	}

	// Macro expansion and scripts draw from one sequence: expanding a macro
	// that binds a name takes a number from the same counter (gensym) reads.
	before := gensyms.Load()
	eval(t, in, `(define-syntax m (syntax-rules () ((m x) (let ((tmp x)) tmp)))) (m 1)`)
	if gensyms.Load() == before {
		t.Error("macro expansion did not draw from the shared gensym counter")
	}
	after := gensyms.Load()
	eval(t, in, `(gensym "tmp")`)
	if n := gensyms.Load(); n <= after {
		t.Errorf("(gensym) did not advance the shared counter: %d -> %d", after, n)
	}
}
