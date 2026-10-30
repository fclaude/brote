package analysis

import (
	"strings"
	"testing"
)

func pos(d *Document, fragment string, occurrence int) Position {
	at := 0
	for i := 0; i <= occurrence; i++ {
		n := strings.Index(d.Text[at:], fragment)
		if n < 0 {
			panic(fragment)
		}
		at += n
		if i < occurrence {
			at += len(fragment)
		}
	}
	return d.Position(at)
}
func TestLexicalScopes(t *testing.T) {
	d := Analyze("(define x 1)\n(define (f x) (+ x 2))\n(f x)", Profile{})
	if len(d.Diagnostics) != 0 {
		t.Fatal(d.Diagnostics)
	}
	outer := d.DefinitionAt(pos(d, "x", 0))
	inner := d.DefinitionAt(pos(d, "x", 1))
	if outer == nil || inner == nil || outer == inner {
		t.Fatal("shadowing lost")
	}
	if d.DefinitionAt(pos(d, "x", 2)) != inner || d.DefinitionAt(pos(d, "x", 3)) != outer {
		t.Fatal("wrong scope")
	}
	if got := len(d.ReferencesAt(pos(d, "x", 0), false)); got != 1 {
		t.Fatal(got)
	}
	if got := len(d.ReferencesAt(pos(d, "f x", 1), true)); got != 2 {
		t.Fatal(got)
	}
}
func TestLetScopesAndRecursion(t *testing.T) {
	for _, kind := range []string{"let", "let*", "letrec"} {
		d := Analyze("(define x 0) ("+kind+" ((x x) (y x)) (+ x y))", Profile{})
		outer := d.DefinitionAt(pos(d, "x", 0))
		binding := d.DefinitionAt(pos(d, "x", 1))
		first := d.DefinitionAt(pos(d, "x", 2))
		second := d.DefinitionAt(pos(d, "x", 3))
		if kind == "letrec" && first != binding || kind != "letrec" && first != outer {
			t.Fatal(kind, "initializer")
		}
		if kind == "let" && second != outer || kind != "let" && second != binding {
			t.Fatal(kind, "second initializer")
		}
	}
	d := Analyze("(define (even n) (odd n)) (define (odd n) (even n))", Profile{})
	if d.DefinitionAt(pos(d, "odd", 0)) == nil {
		t.Fatal("forward definition lost")
	}
}
func TestQuotationCommentsAndIncomplete(t *testing.T) {
	d := Analyze("(define x 1) ; x\n'(x) `(x ,x) #| x |# #(x) (define (f y) (+ y", Profile{})
	if len(d.Diagnostics) != 1 {
		t.Fatal(d.Diagnostics)
	}
	if got := len(d.ReferencesAt(pos(d, "x", 0), false)); got != 1 {
		t.Fatalf("quoted names became references: %d", got)
	}
	if d.DefinitionAt(pos(d, "y", 1)) == nil {
		t.Fatal("incomplete form lost scope")
	}
	d = Analyze("(define s \"😀\") (define x 1) x", Profile{})
	p := pos(d, "x", 1)
	if p.Character != 29 {
		t.Fatalf("UTF16 position: %+v", p)
	}
	if d.DefinitionAt(p) == nil {
		t.Fatal("UTF16 reference lost")
	}
}
func TestHostProfileAndNoExecution(t *testing.T) {
	d := Analyze("(write-file \"/must-not-exist\" \"anything\") (editor-message \"hi\")", Profile{Symbols: []Symbol{{Name: "editor-message", Doc: "Show a message", Parameters: []string{"text"}}}})
	if got := d.Hover(pos(d, "editor-message", 0)); !strings.Contains(got, "Show a message") {
		t.Fatal(got)
	}
	if got := d.Hover(pos(d, "write-file", 0)); got != "" {
		t.Fatal("unexpected builtin")
	}
	if len(d.Completions(Position{})) == 0 {
		t.Fatal("no builtins")
	}
}

func TestDoAndGuardBindings(t *testing.T) {
	d := Analyze("(do ((i 0 (+ i 1))) ((= i 5) i)) (guard (err (else err)) (+ 1 2))", Profile{})
	if d.DefinitionAt(pos(d, "i", 0)) != d.DefinitionAt(pos(d, "i", 2)) {
		t.Fatal("do scope")
	}
	if d.DefinitionAt(pos(d, "err", 0)) != d.DefinitionAt(pos(d, "err", 1)) {
		t.Fatal("guard scope")
	}
}

func FuzzAnalyze(f *testing.F) {
	for _, s := range []string{"(do ((i", "(let ((x", "(guard (e", "(define (f . args) args)", "(define x 1) `(,x x)", "(lambda (x) x)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			t.Skip()
		}
		d := Analyze(s, Profile{})
		for _, def := range d.Definitions {
			if before(def.Range.End, def.Range.Start) {
				t.Fatal(def)
			}
		}
	})
}

func TestMacroDefinitionsAreNavigable(t *testing.T) {
	src := "(define-syntax swap!\n  (syntax-rules ()\n    ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))\n(swap! x y)\n"
	d := Analyze(src, Profile{})
	var def *Definition
	for _, x := range d.Definitions {
		if x.Name == "swap!" {
			def = x
		}
	}
	if def == nil {
		t.Fatalf("no definition of swap!: %+v", d.Definitions)
	}
	if got := d.DefinitionAt(Position{Line: 3, Character: 2}); got != def {
		t.Errorf("the use of swap! does not lead to its definition: %v", got)
	}
	// The pattern variables of the rules are not names in the program.
	for _, r := range d.References {
		if r.Name == "tmp" || r.Name == "a" {
			t.Errorf("a template name was treated as a reference: %+v", r)
		}
	}
	if len(d.Diagnostics) != 0 {
		t.Errorf("diagnostics: %+v", d.Diagnostics)
	}
}

func TestCharactersDoNotUnbalanceTheSyntax(t *testing.T) {
	d := Analyze("(define (f c) (if (char=? c #\\() 1 #\\)))\n(f #\\a)", Profile{})
	if len(d.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", d.Diagnostics)
	}
	var f *Definition
	for _, def := range d.Definitions {
		if def.Name == "f" {
			f = def
		}
	}
	if f == nil || d.DefinitionAt(Position{Line: 1, Character: 1}) != f {
		t.Errorf("the call of f does not lead to its definition")
	}
}

func TestSideEffectWarnings(t *testing.T) {
	// A side effect in a swap! handler warns; nested inside another retried
	// handler it warns too; a pure handler warns nothing.
	cases := []struct{ src, want string }{
		{`(swap! a (lambda (n) (display n) (+ n 1)))`, "swap! may call this procedure more than once"},
		{`(hash-update! h "k" (lambda (n) (hash-update! other "k" (lambda (m) (display m) m) 0) (+ n 1)) 0)`, "hash-update! may call this procedure more than once"},
	}
	for _, c := range cases {
		got := warnings(c.src)
		if len(got) == 0 || !strings.Contains(got[0], c.want) {
			t.Errorf("%s: got %q, want a warning containing %q", c.src, got, c.want)
		}
	}
	for _, src := range []string{
		`(swap! a (lambda (n) (+ n 1)))`,
		`(hash-update! h "k" (lambda (n) (* n 2)) 0)`,
		`(swap! a (lambda (n) (if (> n 0) (+ n 1) 0)))`,
	} {
		if got := warnings(src); len(got) != 0 {
			t.Errorf("%s: unexpected warnings %q", src, got)
		}
	}
}
