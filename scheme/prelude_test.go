package scheme

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func TestPreludeTemplatesArePrivate(t *testing.T) {
	file, err := preparePreludeFile("isolation.scm", `
		(define (cell) '(a . b))
		(define (vec) '#((a . b)))
		(define box '(seed))
		(define (get-box) box)
		(define-syntax literal (syntax-rules () ((literal) '(a . b))))`)
	if err != nil || file.bindings == nil {
		t.Fatalf("prepare declarations: %v", err)
	}
	for i := range 16 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			in := New()
			if err := file.install(in); err != nil {
				t.Fatal(err)
			}
			eval := func(src, want string) {
				t.Helper()
				v, err := in.EvalString(context.Background(), src)
				if err != nil || Write(v) != want {
					t.Fatalf("%s: got %s, %v; want %s", src, Write(v), err, want)
				}
			}
			eval("(list (cell) (vector-ref (vec) 0) (get-box) (literal) (test-results))", "((a . b) (a . b) (seed) (a . b) ())")
			eval(fmt.Sprintf(`(begin
				(set-car! (cell) %[1]d)
				(set-car! (vector-ref (vec) 0) %[1]d)
				(set-car! box %[1]d)
				(test "own" #t)
				(list (cell) (vector-ref (vec) 0) (get-box) (length (test-results))))`, i),
				fmt.Sprintf("((%d . b) (%d . b) (%d) 1)", i, i, i))
			eval("(begin (set! box 'rebound) (get-box))", "rebound")
			// Hosts may inspect and modify a closure through the embedding API.
			fn, _ := in.Global().Lookup("cell")
			fn.Proc.Body[0] = Int(42)
			eval("(cell)", "42")
		})
	}
}

func TestPreludeRunsInitializersInTheirOwnEnvironment(t *testing.T) {
	file, err := preparePreludeFile("initialize.scm", "(define result (list host-value))")
	if err != nil || file.bindings != nil {
		t.Fatalf("prepare initializer: %v", err)
	}
	for i := range 2 {
		in := New()
		in.Global().Define("host-value", Int(int64(i)))
		if err := file.install(in); err != nil {
			t.Fatal(err)
		}
		got, _ := in.Global().Lookup("result")
		if Write(got) != fmt.Sprintf("(%d)", i) {
			t.Fatalf("got %s", Write(got))
		}
	}
}

func TestLazyPreludeBindings(t *testing.T) {
	in := New()
	env := in.Global()
	if !slices.Contains(in.Names(), "identity") {
		t.Fatal("missing prelude name")
	}
	raw, _ := env.global.Load("identity")
	if _, ok := raw.(*preludeBinding); !ok {
		t.Fatal("startup eagerly copied identity")
	}
	// Lookup can race in several evaluations. They must all observe one closure.
	var group sync.WaitGroup
	start := make(chan struct{})
	results := make([]Value, 32)
	for i := range results {
		group.Go(func() { <-start; results[i], _ = env.Lookup("identity") })
	}
	close(start)
	group.Wait()
	for _, v := range results {
		if v.Kind != KProc || v.Proc != results[0].Proc || v.Proc.Env != env {
			t.Fatal("first lookups returned different closures or environments")
		}
	}
	if !env.Set("first", Int(9)) {
		t.Fatal("could not set an unresolved binding")
	}
	env.Define("last", Int(10))
	snapshot := env.Snapshot()
	if snapshot["first"].I != 9 || snapshot["last"].I != 10 || snapshot["identity"].Proc != results[0].Proc || snapshot["compose"].Kind != KProc {
		t.Fatal("snapshot lost a replacement or closure")
	}
	delete(snapshot, "compose")
	env.Restore(snapshot)
	if _, ok := env.Lookup("compose"); ok {
		t.Fatal("restore resurrected a removed template")
	}
}

func BenchmarkInterpreterStartup(b *testing.B) {
	New() // measure subsequent instances in the same process
	b.ReportAllocs()
	for b.Loop() {
		New()
	}
}
