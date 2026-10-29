package scheme

import (
	"context"
	"runtime"
	"sync"
	"testing"
)

const fibSrc = `(define (fib n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))`

func fibInterp(b *testing.B) *Interp {
	b.Helper()
	in := New()
	if _, err := in.EvalString(context.Background(), fibSrc); err != nil {
		b.Fatal(err)
	}
	return in
}

// BenchmarkFib is the serial cost of evaluation: calls, variable lookup,
// arithmetic and allocation.
func BenchmarkFib(b *testing.B) {
	in := fibInterp(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := in.EvalString(context.Background(), "(fib 22)"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFibParallel runs one fib per CPU in one interpreter. Compare its
// time with BenchmarkFib times GOMAXPROCS: the ratio is the speed-up.
func BenchmarkFibParallel(b *testing.B) {
	in := fibInterp(b)
	n := runtime.GOMAXPROCS(0)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := in.EvalString(context.Background(), "(fib 22)"); err != nil {
					panic(err)
				}
			}()
		}
		wg.Wait()
	}
}

// BenchmarkBodies is calls whose bodies define things and loop, the shapes
// fib does not exercise.
func BenchmarkBodies(b *testing.B) {
	in := New()
	if _, err := in.EvalString(context.Background(), `
(define (work n)
  (define a (* n 2))
  (define (step i acc) (if (= i 0) acc (step (- i 1) (+ acc a))))
  (let loop ((i 0) (sum 0))
    (if (= i 50) (+ sum (step 20 0)) (loop (+ i 1) (+ sum i)))))
(define (run) (apply + (map work (iota 200))))`); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := in.EvalString(context.Background(), "(run)"); err != nil {
			b.Fatal(err)
		}
	}
}
