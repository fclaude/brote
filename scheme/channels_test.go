package scheme_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

func TestChannelRendezvous(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define ch (make-channel))
(define producer (spawn (lambda () (for-each (lambda (i) (channel-send! ch i)) (iota 5)) (channel-close! ch))))
(define out '())
(channel-for-each (lambda (v) (set! out (cons v out))) ch)
(task-wait producer)
(reverse out)`))
	if got != "(0 1 2 3 4)" {
		t.Errorf("got %s", got)
	}
}

func TestBufferedChannelDoesNotWaitUntilFull(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	// no receiver exists: only a buffer can take these
	got := scheme.Write(e.ok(t, `
(define ch (make-channel 3))
(channel-send! ch 'a) (channel-send! ch 'b) (channel-send! ch 'c)
(list (channel-recv ch) (channel-recv ch) (channel-try-recv ch 'none) (channel-try-recv ch 'none))`))
	if got != "(a b c none)" {
		t.Errorf("got %s", got)
	}
}

func TestClosedChannels(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define ch (make-channel 2))
(channel-send! ch 1)
(channel-close! ch)
(list (channel-closed? ch) (channel-recv ch 'eof) (channel-recv ch 'eof) (channel-try-recv ch 'none))`))
	if got != "(#t 1 eof none)" {
		t.Errorf("got %s", got)
	}
	e.fails(t, `(define c (make-channel)) (channel-close! c) (channel-send! c 1)`, "send on a closed channel")
	e.fails(t, `(define c (make-channel)) (channel-close! c) (channel-recv c)`, "receive on a closed channel")
	e.fails(t, `(define c (make-channel)) (channel-close! c) (channel-close! c)`, "close of a closed channel")
	// closing wakes tasks waiting on it, receivers and senders both
	got = scheme.Write(e.ok(t, `
(define c (make-channel))
(define rs (map (lambda (i) (spawn (lambda () (channel-recv c 'closed)))) (iota 3)))
(sleep-ms 30)
(channel-close! c)
(task-wait-all rs)`))
	if got != "(closed closed closed)" {
		t.Errorf("got %s", got)
	}
	e.fails(t, `
(define c (make-channel))
(define s (spawn (lambda () (channel-send! c 1))))
(sleep-ms 30)
(channel-close! c)
(task-wait s)`, "send on a closed channel")
}

func TestSelect(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define a (make-channel 1))
(define b (make-channel 1))
(channel-send! b 'from-b)
(select (list (on-recv a) (on-recv b)))`))
	if got != "(1 . from-b)" {
		t.Errorf("only b was ready: %s", got)
	}
	got = scheme.Write(e.ok(t, `
(define a (make-channel))
(select (list (on-recv a)) (lambda () 'nothing))`))
	if got != "(-1 . nothing)" {
		t.Errorf("default: %s", got)
	}
	// a send case, and a select that waits and is woken
	got = scheme.Write(e.ok(t, `
(define a (make-channel))
(define b (make-channel))
(define t1 (spawn (lambda () (sleep-ms 30) (channel-send! b 'late))))
(define r (select (list (on-recv a) (on-recv b))))
(define full (make-channel 1))
(list r (select (list (on-send full 'x))) (channel-recv full))`))
	if got != "((1 . late) (0 . #t) x)" {
		t.Errorf("got %s", got)
	}
	// closed channels yield their eof
	got = scheme.Write(e.ok(t, `(define c (make-channel)) (channel-close! c) (select (list (on-recv c 'done)))`))
	if got != "(0 . done)" {
		t.Errorf("got %s", got)
	}
	e.fails(t, `(select '())`, "wait forever")
	e.fails(t, `(select (list 1 2))`, "on-recv or on-send")
}

// When several cases are ready, each is chosen sometimes.
func TestSelectIsFair(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := e.ok(t, `
(define a (make-channel 1000))
(define b (make-channel 1000))
(for-each (lambda (i) (channel-send! a i) (channel-send! b i)) (iota 500))
(define na 0)
(define nb 0)
(for-each (lambda (i)
  (if (= (car (select (list (on-recv a) (on-recv b)))) 0) (set! na (+ na 1)) (set! nb (+ nb 1))))
  (iota 400))
(list na nb)`)
	items, _ := got.Slice()
	if items[0].I < 100 || items[1].I < 100 {
		t.Errorf("one case was starved: %s", scheme.Write(got))
	}
}

func TestPingPongNeverLooksLikeADeadlock(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define ping (make-channel))
(define pong (make-channel))
(define echo (spawn (lambda ()
  (let loop ()
    (let ((v (channel-recv ping 'stop)))
      (unless (eq? v 'stop) (channel-send! pong (+ v 1)) (loop)))))))
(define total 0)
(do ((i 0 (+ i 1))) ((= i 3000))
  (channel-send! ping i)
  (set! total (+ total (channel-recv pong))))
(channel-close! ping)
(task-wait echo)
total`))
	if got != "4501500" {
		t.Errorf("got %s", got)
	}
}

func TestManyProducersAndConsumers(t *testing.T) {
	e := newTaskInterp(t, scheme.TaskLimits{MaxTasks: 100}, nil)
	got := scheme.Write(e.ok(t, `
(define work (make-channel 4))
(define results (make-channel 4))
(define producers (map (lambda (p) (spawn (lambda ()
   (for-each (lambda (i) (channel-send! work (+ (* p 100) i))) (iota 50))))) (iota 8)))
(define consumers (map (lambda (c) (spawn (lambda ()
   (channel-for-each (lambda (v) (channel-send! results v)) work)))) (iota 6)))
(define closer (spawn (lambda () (task-wait-all producers) (channel-close! work)
                                 (task-wait-all consumers) (channel-close! results))))
(define sum 0)
(define n 0)
(channel-for-each (lambda (v) (set! sum (+ sum v)) (set! n (+ n 1))) results)
(task-wait closer)
(list n sum)`))
	// 8 producers x 50 values; each value is p*100+i
	want := 0
	for p := 0; p < 8; p++ {
		for i := 0; i < 50; i++ {
			want += p*100 + i
		}
	}
	if got != fmt.Sprintf("(400 %d)", want) {
		t.Errorf("got %s, want (400 %d)", got, want)
	}
}

func TestACancelledTaskWakesFromAChannelWait(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	start := time.Now()
	e.ok(t, `
(define c (make-channel))
(define t (spawn (lambda () (channel-recv c))))
(sleep-ms 30)
(task-cancel! t)
(guard (e (#t 'cancelled)) (task-wait t))`)
	if time.Since(start) > 2*time.Second {
		t.Error("cancel did not wake the receiver")
	}
}

func TestTaintCrossesAChannel(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	v := e.ok(t, `(define c (make-channel 1)) (channel-send! c (dirty)) (channel-recv c)`)
	if !v.Taint.Has(scheme.TagFile) {
		t.Errorf("a value lost its provenance through a channel: %v", v.Taint)
	}
}

// --- deadlock ---

func TestDeadlockIsAnErrorNotAHang(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	for name, src := range map[string]string{
		"receive nobody sends": `(channel-recv (make-channel))`,
		"send nobody receives": `(channel-send! (make-channel) 1)`,
		"select":               `(select (list (on-recv (make-channel)) (on-recv (make-channel))))`,
		"wait for oneself":     `(task-wait (current-task))`,
		"wait for a task that waits": `
(define c (make-channel))
(define t (spawn (lambda () (channel-recv c))))
(task-wait t)`,
		"two tasks waiting on each other": `
(define a (make-channel))
(define b (make-channel))
(define t1 (spawn (lambda () (channel-recv a) (channel-send! b 1))))
(define t2 (spawn (lambda () (channel-recv b) (channel-send! a 1))))
(task-wait-all (list t1 t2))`,
	} {
		done := make(chan error, 1)
		go func() { _, err := e.eval(src); done <- err }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "deadlock") {
				t.Errorf("%s: got %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: hung instead of reporting a deadlock", name)
		}
	}
	// A script can catch it, and the interpreter is usable afterwards.
	got := scheme.Write(e.ok(t, `(guard (e (#t 'recovered)) (channel-recv (make-channel)))`))
	if got != "recovered" {
		t.Errorf("got %s", got)
	}
	e.ok(t, `(+ 1 1)`)
}

// A task in a host call, or a timer, will wake the others, so waiting for it
// is not a deadlock.
func TestWaitingForAHostCallOrATimerIsNotADeadlock(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	got := scheme.Write(e.ok(t, `
(define c (make-channel))
(spawn (lambda () (sleep-ms 150) (channel-send! c 'from-sleeper)))
(channel-recv c)`))
	if got != "from-sleeper" {
		t.Errorf("got %s", got)
	}
	start := time.Now()
	got = scheme.Write(e.ok(t, `(channel-recv (after 0.15))`))
	if got != "#t" || time.Since(start) < 100*time.Millisecond {
		t.Errorf("after: %s in %v", got, time.Since(start))
	}
	// the timeout pattern
	got = scheme.Write(e.ok(t, `
(define slow (make-channel))
(car (select (list (on-recv slow) (on-recv (after 0.05)))))`))
	if got != "1" {
		t.Errorf("timeout: %s", got)
	}
}

func TestATimerDoesNotOutliveItsEvaluation(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	e.ok(t, `(after 3600) 'done`)
	// had the hour-long timer been left pending, this would wait for it
	// rather than report the deadlock
	e.fails(t, `(channel-recv (make-channel))`, "deadlock")
}

func TestSeveralEvaluationsDeadlockSeparately(t *testing.T) {
	e := newTaskInterp(t, defaultLimits(), nil)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { _, err := e.eval(`(channel-recv (make-channel))`); errs <- err }()
	}
	for i := 0; i < 4; i++ {
		select {
		case err := <-errs:
			if err == nil || !strings.Contains(err.Error(), "deadlock") {
				t.Errorf("got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("hung")
		}
	}
}
