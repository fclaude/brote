package scheme_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

// files writes name -> text under a new directory and returns it.
func files(t *testing.T, m map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range m {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func evalIn(t *testing.T, in *scheme.Interp, src string) (string, error) {
	t.Helper()
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		return "", err
	}
	return scheme.Write(v), nil
}

const stringsModule = `(export shout twice)
(define uses 0)
(define (helper s) (string-append s "!"))
(define (shout s) (set! uses (+ uses 1)) (helper (string-upcase s)))
(define-syntax twice (syntax-rules () ((_ e) (list e e))))
(set! evaluations (+ evaluations 1))
`

func TestImportGivesWhatAFileExports(t *testing.T) {
	dir := files(t, map[string]string{
		"lib/strings.scm": stringsModule,
		"lib/math.scm":    "(define (square x) (* x x))\n(define pi 3)\n",
		"lib/uses.scm":    "(import \"strings.scm\" shout)\n(export loud)\n(define (loud s) (shout (string-append s \" now\")))\n",
	})
	in := scheme.New(scheme.WithBaseDir(dir))
	if _, err := evalIn(t, in, "(define evaluations 0)"); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{`(begin (import "lib/strings.scm") (shout "hi"))`: `"HI!"`,
		`(begin (import "lib/strings.scm") (twice 1))`:             `(1 1)`,
		`(begin (import "lib/math.scm") (list (square 3) pi))`:     `(9 3)`,
		`(begin (import "lib/math.scm" (prefix m:)) (m:square 5))`: `25`,
		`(begin (import "lib/math.scm" pi (prefix m-)) m-pi)`:      `3`,
		`(begin (import "lib/uses.scm") (loud "go"))`:              `"GO NOW!"`,
		`(let () (import "lib/math.scm" square) (square 2))`:       `4`,
		`(begin (define (f) (import "lib/math.scm" pi) pi) (f))`:   `3`,
	} {
		if got, err := evalIn(t, in, src); err != nil || got != want {
			t.Errorf("%s\n got  %s, %v\n want %s", src, got, err, want)
		}
	}
	// Private names stay in the module, and so do names imported by name only.
	for _, name := range []string{"helper", "uses", "m-square"} {
		if _, err := evalIn(t, in, name); err == nil || !strings.Contains(err.Error(), "unbound variable") {
			t.Errorf("%s is visible after the imports: %v", name, err)
		}
	}
	// The file ran once, for every import of it, from every file.
	if got, err := evalIn(t, in, "evaluations"); err != nil || got != "1" {
		t.Errorf("lib/strings.scm was evaluated %s times, %v", got, err)
	}
}

// A relative path in a file is taken against that file's directory, wherever
// the program was started from.
func TestImportIsRelativeToTheImportingFile(t *testing.T) {
	dir := files(t, map[string]string{
		"app/main.scm":     "(import \"lib/a.scm\")\n(define result (a))\n",
		"app/lib/a.scm":    "(import \"../shared/b.scm\")\n(define (a) (list 'a (b)))\n",
		"app/shared/b.scm": "(define (b) 'b)\n",
	})
	in := scheme.New(scheme.WithBaseDir(t.TempDir()))
	if err := in.LoadFile(context.Background(), filepath.Join(dir, "app", "main.scm")); err != nil {
		t.Fatal(err)
	}
	if got, err := evalIn(t, in, "result"); err != nil || got != "(a b)" {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestImportErrors(t *testing.T) {
	dir := files(t, map[string]string{
		"lib/strings.scm": stringsModule,
		"cycle/a.scm":     "(import \"b.scm\")\n",
		"cycle/b.scm":     "(import \"a.scm\")\n",
		"bad.scm":         "(export nothere)\n(define x 1)\n",
		"nested.scm":      "(define (f) (export y))\n(define y 1)\n(f)\n",
		"broken.scm":      "(define fine 1)\n(car 5)\n",
	})
	in := scheme.New(scheme.WithBaseDir(dir))
	if _, err := evalIn(t, in, "(define evaluations 0)"); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		`(import "lib/strings.scm" helper)`: "does not export helper",
		`(import "cycle/a.scm")`:            "import cycle",
		`(import "bad.scm")`:                "exports nothere, which it does not define",
		`(import "nested.scm")`:             "top level",
		`(export x)`:                        "top level of a file that is imported",
		`(import "missing.scm")`:            "missing.scm",
		`(import (scheme base))`:            "Brote imports files",
		`(import 5)`:                        "expected a file name",
		`(import "lib/strings.scm" "x")`:    "expected a name or (prefix p)",
		`(import "broken.scm")`:             "car",
	} {
		if _, err := evalIn(t, in, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", src, err, want)
		}
	}
	// The error says where in the module it happened.
	_, err := in.EvalNamed(context.Background(), "<expr>", `(import "broken.scm")`)
	if !strings.Contains(scheme.FormatError(err), "broken.scm:2:1") {
		t.Errorf("error %q does not name the module's line", scheme.FormatError(err))
	}
	// A file that failed is tried again by the next import.
	if err := os.WriteFile(filepath.Join(dir, "broken.scm"), []byte("(define fine 2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := evalIn(t, in, `(begin (import "broken.scm") fine)`); err != nil || got != "2" {
		t.Errorf("after the fix: %s, %v", got, err)
	}
}

// The gate is asked about the file an import reads, as it is about load's,
// and sees the path the import resolved.
func TestImportGoesThroughTheGate(t *testing.T) {
	dir := files(t, map[string]string{"lib/a.scm": "(import \"b.scm\")\n", "lib/b.scm": "(define b 1)\n"})
	in := scheme.New(scheme.WithBaseDir(dir))
	g := &recordGate{}
	in.SetGate(g)
	if _, err := evalIn(t, in, `(import "lib/a.scm")`); err != nil {
		t.Fatal(err)
	}
	want := []string{"import lib/a.scm", "import " + filepath.Join(dir, "lib", "b.scm")}
	if strings.Join(g.calls, "|") != strings.Join(want, "|") {
		t.Errorf("gate saw %q, want %q", g.calls, want)
	}
	in = scheme.New(scheme.WithBaseDir(dir))
	in.SetGate(&recordGate{deny: "import"})
	_, err := evalIn(t, in, `(guard (e (#t 'caught)) (import "lib/b.scm"))`)
	var d *scheme.Denied
	if err == nil || !errors.As(err, &d) {
		t.Errorf("a denied import: %v, want an uncatchable denial", err)
	}
}

// recordGate notes each primitive it is asked about, with its first
// argument, and denies the one named deny.
type recordGate struct {
	deny  string
	calls []string
}

func (g *recordGate) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if len(args) > 0 {
		g.calls = append(g.calls, p.Name+" "+scheme.Display(args[0]))
	}
	if p.Name == g.deny {
		return &scheme.Denied{Prim: p.Name, Reason: "not here"}
	}
	return nil
}

func (g *recordGate) RegisterChecker(string, string, scheme.Value) error { return nil }
