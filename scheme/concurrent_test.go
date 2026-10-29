package scheme

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// These tests run goroutines against one Interp. Until tasks exist they are
// how the interpreter's shared state is shown to be safe: the race detector
// is the oracle (go test -race), and a plain run catches the lost updates it
// cannot see. A failure of the first kind can end the process with
// "concurrent map writes", which is the thing they guard against.

const goroutines = 16

// parallel runs fn on n goroutines at once and waits for them.
func parallel(t *testing.T, n int, fn func(g int)) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn(g)
		}()
	}
	close(start)
	wg.Wait()
}

func evalOK(t *testing.T, in *Interp, src string) Value {
	t.Helper()
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		t.Errorf("%s: %v", src, err)
	}
	return v
}

func TestConcurrentGlobalDefinesAndReads(t *testing.T) {
	in := New()
	parallel(t, goroutines, func(g int) {
		for i := 0; i < 50; i++ {
			evalOK(t, in, fmt.Sprintf("(define v%d-%d %d)", g, i, i))
			evalOK(t, in, fmt.Sprintf("(+ v%d-%d 1)", g, i))
		}
	})
	if v := evalOK(t, in, "(+ v3-10 v15-49)"); v.I != 59 {
		t.Errorf("got %s", Write(v))
	}
}

// A frame captured by a closure is shared the moment the closure is. This is
// the case that crashes an unlocked frame: one goroutine defines in a frame
// while another reads it.
func TestConcurrentSharedFrame(t *testing.T) {
	in := New()
	evalOK(t, in, `
(define (make-counter)
  (define n 0)
  (define (bump!) (set! n (+ n 1)) n)
  (define (peek) n)
  (cons bump! peek))
(define c (make-counter))`)
	parallel(t, goroutines, func(g int) {
		for i := 0; i < 100; i++ {
			evalOK(t, in, "((car c))")
			evalOK(t, in, "((cdr c))")
			evalOK(t, in, fmt.Sprintf("(let () (define late%d 1) (+ late%d 1))", g, g))
		}
	})
	// set! is atomic, but the read-then-write inside bump! is not: updates
	// may be lost, never torn.
	if v := evalOK(t, in, "((cdr c))"); v.Kind != KInt || v.I < 1 || v.I > goroutines*100 {
		t.Errorf("counter = %s", Write(v))
	}
}

func TestConcurrentVector(t *testing.T) {
	in := New()
	evalOK(t, in, "(define v (vector 0 0 0 0))")
	parallel(t, goroutines, func(g int) {
		for i := 0; i < 200; i++ {
			evalOK(t, in, fmt.Sprintf("(vector-set! v %d %d)", i%4, g))
			evalOK(t, in, fmt.Sprintf("(vector-ref v %d)", (i+1)%4))
			evalOK(t, in, "(vector->list v)")
			evalOK(t, in, "(equal? v (list->vector (vector->list v)))")
			evalOK(t, in, "(vector-length v)")
		}
	})
	if got := evalOK(t, in, "(vector-length v)"); got.I != 4 {
		t.Errorf("length %s", Write(got))
	}
}

func TestConcurrentHash(t *testing.T) {
	in := New()
	evalOK(t, in, "(define h (make-hash))")
	parallel(t, goroutines, func(g int) {
		for i := 0; i < 200; i++ {
			k := fmt.Sprintf("k%d", i%32)
			evalOK(t, in, fmt.Sprintf(`(hash-set! h "%s" %d)`, k, g))
			evalOK(t, in, fmt.Sprintf(`(hash-ref h "%s" #f)`, k))
			evalOK(t, in, "(hash-keys h)")
			evalOK(t, in, "(hash->alist h)")
			evalOK(t, in, "(hash-count h)")
			if i%7 == 0 {
				evalOK(t, in, fmt.Sprintf(`(hash-remove! h "%s")`, k))
			}
			evalOK(t, in, "(write h)")
		}
	})
}

// hash-update! is atomic: no increment is lost however the goroutines
// interleave, even though the procedure runs outside the table's lock and
// may run more than once.
func TestHashUpdateLosesNothing(t *testing.T) {
	in := New()
	evalOK(t, in, "(define h (make-hash))")
	const per = 300
	parallel(t, goroutines, func(g int) {
		for i := 0; i < per; i++ {
			evalOK(t, in, `(hash-update! h "n" (lambda (n) (+ n 1)) 0)`)
			evalOK(t, in, fmt.Sprintf(`(hash-update! h "g%d" (lambda (n) (+ n 1)) 0)`, g%4))
		}
	})
	if v := evalOK(t, in, `(hash-ref h "n")`); v.I != goroutines*per {
		t.Errorf("n = %s, want %d", Write(v), goroutines*per)
	}
	total := evalOK(t, in, `(+ (hash-ref h "g0") (hash-ref h "g1") (hash-ref h "g2") (hash-ref h "g3"))`)
	if total.I != goroutines*per {
		t.Errorf("per-group total = %s, want %d", Write(total), goroutines*per)
	}
}

// An update that races a remove and a re-add of the same key must not be
// applied to the new entry as if nothing had happened.
func TestHashUpdateSeesRemoveAndReadd(t *testing.T) {
	h := NewHash().Hash
	k, _ := keyOf(Str("k"))
	h.set(k, Str("k"), Int(1))
	_, ver, ok := h.snapshot(k)
	if !ok {
		t.Fatal("no entry")
	}
	h.remove(k)
	h.set(k, Str("k"), Int(1)) // same value, new write
	if h.setIfUnchanged(k, Str("k"), Int(2), ver, true) {
		t.Error("update applied over a remove and re-add")
	}
	_, ver, _ = h.snapshot(k)
	if !h.setIfUnchanged(k, Str("k"), Int(2), ver, true) {
		t.Error("update refused with nothing in between")
	}
	h.remove(k)
	if h.setIfUnchanged(k, Str("k"), Int(3), ver, true) {
		t.Error("update applied to a removed entry")
	}
	if !h.setIfUnchanged(k, Str("k"), Int(3), 0, false) {
		t.Error("insert refused for an absent key")
	}
	if h.setIfUnchanged(k, Str("k"), Int(4), 0, false) {
		t.Error("insert applied over an entry that appeared")
	}
}

// Iterating a table while procedures called from inside the walk change it:
// each walks a copy, so this neither deadlocks nor loses its place.
func TestHashWalkMayChangeTheTable(t *testing.T) {
	in := New()
	evalOK(t, in, `(define h (make-hash))
(for-each (lambda (i) (hash-set! h i i)) (iota 20))`)
	parallel(t, 4, func(g int) {
		for i := 0; i < 50; i++ {
			evalOK(t, in, `(for-each (lambda (k) (hash-set! h k 0) (hash-remove! h (+ k 1))) (hash-keys h))`)
			evalOK(t, in, `(hash-update! h 3 (lambda (x) (hash-set! h 4 4) x) 0)`)
		}
	})
}

// Each goroutine's with-output-to-string captures only its own output, and
// plain output from several reaches the host's writer whole.
func TestConcurrentOutput(t *testing.T) {
	var out strings.Builder // not safe for concurrent use: the interpreter must serialise
	in := New(WithOutput(&out))
	parallel(t, goroutines, func(g int) {
		for i := 0; i < 50; i++ {
			s := evalOK(t, in, fmt.Sprintf(`(with-output-to-string (display "g%d") (display %d))`, g, i))
			if want := fmt.Sprintf("g%d%d", g, i); s.S != want {
				t.Errorf("captured %q, want %q", s.S, want)
			}
			// One display is one write, so one line; display then newline
			// are two writes and may be separated by another goroutine's.
			evalOK(t, in, fmt.Sprintf(`(display "<line %d>\n")`, g))
		}
	})
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.HasPrefix(line, "<line ") || !strings.HasSuffix(line, ">") {
			t.Fatalf("output interleaved: %q", line)
		}
	}
}

// Recursion depth is counted per evaluation: deep recursion in one goroutine
// does not eat another's budget, and each still gets the error at its own limit.
func TestDepthIsPerEvaluation(t *testing.T) {
	in := New()
	evalOK(t, in, `(define (down n) (if (= n 0) 0 (+ 1 (down (- n 1)))))`)
	parallel(t, goroutines, func(g int) {
		// Each needs well over half of MaxDepth, so sharing one counter
		// would make most of them fail.
		v, err := in.EvalString(context.Background(), fmt.Sprintf("(down %d)", MaxDepth/2))
		if err != nil || v.I != MaxDepth/2 {
			t.Errorf("down: %v %v", Write(v), err)
		}
		if _, err := in.EvalString(context.Background(), fmt.Sprintf("(down %d)", MaxDepth*2)); err == nil || !strings.Contains(err.Error(), "too deep") {
			t.Errorf("limit: %v", err)
		}
	})
}

// A handler that calls back into Scheme stays in the evaluation that called
// it, so the depth it nests counts against that evaluation's limit.
func TestCallbackDepthCountsAgainstTheCaller(t *testing.T) {
	in := New()
	evalOK(t, in, `(define (f n) (if (= n 0) 0 (+ 1 (car (map f (list (- n 1)))))))`)
	if _, err := in.EvalString(context.Background(), fmt.Sprintf("(f %d)", MaxDepth)); err == nil || !strings.Contains(err.Error(), "too deep") {
		t.Errorf("recursion through map was not bounded: %v", err)
	}
}

func TestConcurrentRegisterAndLookup(t *testing.T) {
	in := New()
	parallel(t, goroutines, func(g int) {
		for i := 0; i < 50; i++ {
			err := in.Register(&Prim{
				Name:    fmt.Sprintf("p%d-%d", g, i),
				Meta:    Meta{Doc: "test"},
				Handler: func(c *Call) (Value, error) { return Int(1), nil },
			})
			if err != nil {
				t.Error(err)
			}
			in.Lookup("car")
			in.Prims()
			evalOK(t, in, "(primitives)")
			evalOK(t, in, fmt.Sprintf("(p%d-%d)", g, i))
		}
	})
}

// A closure that define names after it exists is read by printing and by
// arity errors in other goroutines.
func TestConcurrentClosureNaming(t *testing.T) {
	in := New()
	for round := 0; round < 20; round++ {
		evalOK(t, in, "(define anon (lambda (x) x))")
		parallel(t, 8, func(g int) {
			evalOK(t, in, fmt.Sprintf("(define alias%d anon)", g))
			evalOK(t, in, "(write anon)")
			if _, err := in.EvalString(context.Background(), "(anon)"); err == nil {
				t.Error("arity error expected")
			}
		})
	}
}

// Many goroutines evaluating the same code at once, with no shared writes
// beyond the frames each call makes, is the ordinary case and must scale
// without error.
func TestConcurrentEvaluation(t *testing.T) {
	in := New()
	evalOK(t, in, `(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))`)
	parallel(t, goroutines, func(g int) {
		if v := evalOK(t, in, "(fib 15)"); v.I != 610 {
			t.Errorf("fib 15 = %s", Write(v))
		}
	})
}

func TestSnapshotAndRestoreOfTheGlobalFrame(t *testing.T) {
	in := New()
	evalOK(t, in, "(define x 1)")
	snap := in.Global().Snapshot()
	evalOK(t, in, "(define x 2) (define y 3)")
	in.Global().Restore(snap)
	if v := evalOK(t, in, "x"); v.I != 1 {
		t.Errorf("x = %s", Write(v))
	}
	if _, err := in.EvalString(context.Background(), "y"); err == nil {
		t.Error("y survived the restore")
	}
	local := NewEnv(in.Global())
	local.Define("a", Int(1))
	snap = local.Snapshot()
	local.Define("b", Int(2))
	local.Restore(snap)
	if _, ok := local.Lookup("b"); ok {
		t.Error("b survived the restore of a local frame")
	}
	local.Restore(nil)
	local.Define("c", Int(3)) // a restored frame still takes definitions
}
