package analysis

import (
	"strings"
	"testing"
)

func warnings(src string) []string {
	var out []string
	for _, d := range Analyze(src, Profile{}).Diagnostics {
		if d.Severity == warning {
			out = append(out, d.Message)
		}
	}
	return out
}

func TestWarnsOnSharedWritesFromATask(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"global read-modify-write": {`(define n 0) (spawn (lambda () (set! n (+ n 1))))`, "n is read and then set!"},
		"global plain write":       {`(define flag #f) (pfor-each (lambda (x) (set! flag x)) '(1 2))`, "flag is set! from a task"},
		"captured local":           {`(define (count xs) (let ((total 0)) (pfor-each (lambda (x) (set! total (+ total x))) xs) total))`, "total is read and then set!"},
		"in a detached task":       {`(define n 0) (spawn-detached (lambda () (set! n (+ n 1))))`, "n is read and then set!"},
		"in a group":               {`(define n 0) (task-group (lambda (g) (group-spawn g (lambda () (set! n 1)))))`, "n is set! from a task"},
		"in pmap-limit":            {`(define n 0) (pmap-limit 4 (lambda (x) (set! n x)) '(1 2))`, "n is set! from a task"},
		"in race":                  {`(define n 0) (race (list (lambda () (set! n 1)) (lambda () 2)))`, "n is set! from a task"},
	} {
		got := warnings(c.src)
		if len(got) != 1 || !strings.Contains(got[0], c.want) {
			t.Errorf("%s: got %q, want one containing %q", name, got, c.want)
		}
	}
}

func TestNoWarningForATasksOwnVariables(t *testing.T) {
	for name, src := range map[string]string{
		"parameter":      `(pmap (lambda (x) (set! x (+ x 1)) x) '(1 2))`,
		"let inside":     `(spawn (lambda () (let ((n 0)) (set! n (+ n 1)) n)))`,
		"define inside":  `(spawn (lambda () (define n 0) (set! n 5) n))`,
		"named let loop": `(spawn (lambda () (let loop ((i 0)) (if (< i 3) (loop (+ i 1)) i))))`,
		"outside a task": `(define n 0) (set! n (+ n 1))`,
		"an atom":        `(define n (make-atom 0)) (pfor-each (lambda (x) (swap! n (lambda (v) (+ v 1)))) '(1 2))`,
		"not a thunk":    `(define n 0) (spawn work) (define (work) (set! n 1))`,
	} {
		if got := warnings(src); len(got) != 0 {
			t.Errorf("%s: unexpected warnings %q", name, got)
		}
	}
}

func TestWarnsOnWaitingWhileHoldingALock(t *testing.T) {
	got := warnings(`(define m (make-mutex)) (with-lock m (lambda () (channel-recv ch)))`)
	if len(got) != 1 || !strings.Contains(got[0], "channel-recv waits for another task") {
		t.Errorf("got %q", got)
	}
	if got := warnings(`(define m (make-mutex)) (with-lock m (lambda () (vector-set! v 0 1)))`); len(got) != 0 {
		t.Errorf("unexpected %q", got)
	}
}

func TestWarnsOnEffectsInARetriedProcedure(t *testing.T) {
	got := warnings(`(swap! a (lambda (n) (display n) (+ n 1)))`)
	if len(got) != 1 || !strings.Contains(got[0], "swap! may call this procedure more than once") {
		t.Errorf("got %q", got)
	}
	got = warnings(`(hash-update! h "k" (lambda (n) (hash-set! other "k" n) (+ n 1)) 0)`)
	if len(got) != 1 || !strings.Contains(got[0], "hash-set!") {
		t.Errorf("got %q", got)
	}
	if got := warnings(`(swap! a (lambda (n) (+ n 1))) (hash-update! h "k" (lambda (n) (* n 2)) 1)`); len(got) != 0 {
		t.Errorf("unexpected %q", got)
	}
}

func TestTaskProceduresAreKnownToTheEditor(t *testing.T) {
	d := Analyze("(spawn (lambda () 1))", Profile{})
	if got := d.Hover(pos(d, "spawn", 0)); !strings.Contains(got, "thunk") {
		t.Errorf("hover = %q", got)
	}
}
