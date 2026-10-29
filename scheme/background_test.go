package scheme_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

// bgEnv is an interpreter with detached tasks granted and a few primitives
// to test them with: sleep-ms, hold (waits for its context), boom (fails),
// stubborn (ignores its context until release is closed) and ctx-tag (the
// value the host put in the context, under tagKey).
type bgEnv struct {
	in      *scheme.Interp
	holding atomic.Int64
	release chan struct{}
	failed  chan failure
}

type failure struct {
	task scheme.Task
	err  error
}

type tagKeyType struct{}

var tagKey tagKeyType

func newBgInterp(t *testing.T, limits scheme.TaskLimits, opts ...scheme.Option) *bgEnv {
	t.Helper()
	e := &bgEnv{release: make(chan struct{}), failed: make(chan failure, 16)}
	limits.Background = true
	opts = append([]scheme.Option{
		scheme.WithTasks(limits),
		scheme.WithTaskFailed(func(tk scheme.Task, err error) { e.failed <- failure{tk, err} }),
	}, opts...)
	e.in = scheme.New(opts...)
	reg := func(name string, params []scheme.Param, h func(c *scheme.Call) (scheme.Value, error)) {
		if err := e.in.Register(&scheme.Prim{Name: name, Meta: scheme.Meta{Doc: name + " (test)", Params: params}, Handler: h}); err != nil {
			t.Fatal(err)
		}
	}
	reg("sleep-ms", []scheme.Param{{Name: "ms", Type: "integer"}}, func(c *scheme.Call) (scheme.Value, error) {
		ms, err := c.Int(0)
		if err != nil {
			return scheme.Value{}, err
		}
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return scheme.Void, nil
		case <-c.Ctx.Done():
			return scheme.Value{}, c.Ctx.Err()
		}
	})
	reg("hold", nil, func(c *scheme.Call) (scheme.Value, error) {
		e.holding.Add(1)
		defer e.holding.Add(-1)
		<-c.Ctx.Done()
		return scheme.Value{}, c.Ctx.Err()
	})
	reg("boom", nil, func(c *scheme.Call) (scheme.Value, error) { return scheme.Value{}, errors.New("it broke") })
	reg("stubborn", nil, func(c *scheme.Call) (scheme.Value, error) {
		e.holding.Add(1)
		defer e.holding.Add(-1)
		<-e.release
		return scheme.Void, nil
	})
	reg("ctx-tag", nil, func(c *scheme.Call) (scheme.Value, error) {
		s, _ := c.Ctx.Value(tagKey).(string)
		return scheme.Str(s), nil
	})
	return e
}

func (e *bgEnv) evalCtx(ctx context.Context, src string) (scheme.Value, error) {
	return e.in.EvalString(ctx, src)
}

func (e *bgEnv) ok(t *testing.T, src string) scheme.Value {
	t.Helper()
	v, err := e.evalCtx(context.Background(), src)
	if err != nil {
		t.Fatalf("%s\n  error: %v", src, err)
	}
	return v
}

func (e *bgEnv) fails(t *testing.T, src, contains string) {
	t.Helper()
	_, err := e.evalCtx(context.Background(), src)
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("%s\n  error %v, want one containing %q", src, err, contains)
	}
}

func (e *bgEnv) global(name string) scheme.Value {
	v, _ := e.in.Global().Lookup(name)
	return v
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func shutdown(t *testing.T, in *scheme.Interp) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := in.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// 1. A detached task outlives its evaluation and is awaited from the next.
func TestADetachedTaskOutlivesItsEvaluationAndIsAwaitedFromTheNext(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	start := time.Now()
	e.ok(t, `(define job (spawn-detached (lambda () (sleep-ms 150) 'finished-later)))`)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("the evaluation waited for the detached task")
	}
	// A different evaluation sees it still running, and can wait for it.
	if got := scheme.Write(e.ok(t, `(task-done? job)`)); got != "#f" {
		t.Errorf("task-done? = %s while it should be running", got)
	}
	if got := scheme.Write(e.ok(t, `(task-wait job)`)); got != "finished-later" {
		t.Errorf("task-wait = %s", got)
	}
	if got := scheme.Write(e.ok(t, `(task-done? job)`)); got != "#t" {
		t.Errorf("task-done? = %s after it finished", got)
	}
	// Waiting again gives the same answer.
	if got := scheme.Write(e.ok(t, `(task-wait job)`)); got != "finished-later" {
		t.Errorf("second task-wait = %s", got)
	}
}

func TestDetachedTasksNeedTheGrant(t *testing.T) {
	in := scheme.New(scheme.WithTasks(scheme.TaskLimits{}))
	for _, name := range []string{"spawn-detached", "after-detached"} {
		if _, ok := in.Lookup(name); ok {
			t.Errorf("%s is defined without TaskLimits.Background", name)
		}
	}
	if err := in.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown without a grant: %v", err)
	}
	if err := scheme.New().Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown without tasks: %v", err)
	}
}

func TestADetachedTaskIsNotCancelledWithItsSpawner(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.evalCtx(ctx, `(define job (spawn-detached (lambda () (sleep-ms 100) 'alive)))`); err != nil {
		t.Fatal(err)
	}
	cancel() // the command's context ends; the detached task is not the command's
	if got := scheme.Write(e.ok(t, `(task-wait job)`)); got != "alive" {
		t.Errorf("got %s", got)
	}
}

func TestADetachedTaskCanBeCancelledFromALaterEvaluation(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define job (spawn-detached (lambda () (hold))))`)
	waitFor(t, "the task to start", func() bool { return e.holding.Load() == 1 })
	e.ok(t, `(task-cancel! job)`)
	waitFor(t, "the task to stop", func() bool { return e.holding.Load() == 0 })
	e.fails(t, `(task-wait job)`, "task")
	select {
	case f := <-e.failed:
		t.Errorf("a cancelled task was reported as failed: %v", f.err)
	default:
	}
}

func TestADetachedTasksOwnTasksEndWithIt(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define job (spawn-detached (lambda () (let ((c (spawn (lambda () (hold))))) (task-wait c)))))`)
	waitFor(t, "the inner task to start", func() bool { return e.holding.Load() == 1 })
	e.ok(t, `(task-cancel! job)`)
	waitFor(t, "both to stop", func() bool { return e.holding.Load() == 0 })
	// It is its own evaluation: it ending takes the tasks it started with it.
	e.ok(t, `(define job2 (spawn-detached (lambda () (spawn (lambda () (hold))) 'returned)))`)
	if got := scheme.Write(e.ok(t, `(task-wait job2)`)); got != "returned" {
		t.Errorf("got %s", got)
	}
	waitFor(t, "the orphan to be cancelled", func() bool { return e.holding.Load() == 0 })
}

func TestDetachedTasksCountAgainstMaxTasks(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{MaxTasks: 2})
	defer shutdown(t, e.in)
	e.ok(t, `(spawn-detached (lambda () (hold)))`)
	e.ok(t, `(define second (spawn-detached (lambda () (hold))))`)
	e.fails(t, `(spawn-detached (lambda () 1))`, "too many tasks")
	e.fails(t, `(spawn (lambda () 1))`, "too many tasks")
	e.ok(t, `(task-cancel! second)`)
	waitFor(t, "room", func() bool { return e.holding.Load() == 1 })
	waitFor(t, "the slot to free", func() bool {
		_, err := e.evalCtx(context.Background(), `(task-wait (spawn (lambda () 1)))`)
		return err == nil
	})
}

// A deadlock in an evaluation is the evaluation's: it does not take the
// detached tasks, which wait for the outside, with it.
func TestADeadlockInAnEvaluationLeavesDetachedTasksAlone(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define q (make-channel 1))
(define job (spawn-detached (lambda () (channel-recv q))))`)
	e.fails(t, `(channel-recv (make-channel))`, "deadlock")
	if got := scheme.Write(e.ok(t, `(task-done? job)`)); got != "#f" {
		t.Fatalf("the detached task was ended by someone else's deadlock: done? = %s", got)
	}
	e.ok(t, `(channel-send! q 'later)`)
	if got := scheme.Write(e.ok(t, `(task-wait job)`)); got != "later" {
		t.Errorf("got %s", got)
	}
}

// 1 (continued). Shutdown cancels every detached task and waits for them.
func TestShutdownCancelsEveryDetachedTaskAndWaits(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	e.ok(t, `(define jobs (map (lambda (i) (spawn-detached (lambda () (hold)))) (iota 5)))
(define timer (after-detached 3600))`)
	waitFor(t, "five tasks", func() bool { return e.holding.Load() == 5 })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.in.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.holding.Load(); n != 0 {
		t.Fatalf("%d tasks still running after Shutdown returned", n)
	}
	// Every handle says so, from a later evaluation.
	e.fails(t, `(task-wait (car jobs))`, "interpreter shut down")
	if got := scheme.Write(e.ok(t, `(every task-done? jobs)`)); got != "#t" {
		t.Errorf("not all done: %s", got)
	}
	// The timer's channel was closed rather than left to hang its waiters.
	if got := scheme.Write(e.ok(t, `(channel-closed? timer)`)); got != "#t" {
		t.Errorf("timer channel closed? %s", got)
	}
	// Nothing new starts; evaluations otherwise still work.
	e.fails(t, `(spawn-detached (lambda () 1))`, "shutting down")
	e.fails(t, `(after-detached 1)`, "shutting down")
	e.ok(t, `(task-wait (spawn (lambda () 1)))`)
	// Cancellation is not failure.
	select {
	case f := <-e.failed:
		t.Errorf("Shutdown's cancellation was reported as a failure: %v", f.err)
	default:
	}
	// And it can be called again.
	if err := e.in.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

func TestShutdownIsBoundedByItsContext(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	e.ok(t, `(spawn-detached (lambda () (stubborn)))`)
	waitFor(t, "the task to start", func() bool { return e.holding.Load() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := e.in.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "1 detached task") {
		t.Fatalf("Shutdown = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Shutdown took %v", time.Since(start))
	}
	close(e.release)
	shutdown(t, e.in) // now it can finish
	if e.holding.Load() != 0 {
		t.Error("the task is still running")
	}
}

// Shutdown from the goroutine of an evaluation that is spawning is safe.
func TestShutdownRacesWithSpawning(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{MaxTasks: 1000})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				e.evalCtx(context.Background(), `(spawn-detached (lambda () (sleep-ms 5)))`)
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	shutdown(t, e.in)
	wg.Wait()
	shutdown(t, e.in)
}

// 1 (continued). A failure nobody waited for goes to the host, not to an
// unrelated evaluation.
func TestAnUnobservedDetachedFailureGoesToTheHostHook(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define job (spawn-detached (lambda () (sleep-ms 30) (boom))))`)
	// Unrelated evaluations while it fails are not touched.
	for i := 0; i < 5; i++ {
		e.ok(t, `(+ 1 1)`)
		time.Sleep(15 * time.Millisecond)
	}
	var f failure
	select {
	case f = <-e.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("the host was never told")
	}
	if f.err == nil || !strings.Contains(f.err.Error(), "it broke") {
		t.Errorf("hook error = %v", f.err)
	}
	if v, err := f.task.Result(); err == nil || !strings.Contains(err.Error(), "it broke") {
		t.Errorf("Result = %s, %v", scheme.Write(v), err)
	}
	if want := e.global("job"); scheme.Write(f.task.Value()) != scheme.Write(want) {
		t.Errorf("hook task %s is not the script's %s", scheme.Write(f.task.Value()), scheme.Write(want))
	}
	e.ok(t, `(+ 1 1)`) // still fine
	// The handle still reports it to anyone who asks later.
	e.fails(t, `(task-wait job)`, "it broke")
}

func TestAFailureSomeoneIsWaitingForIsTheWaitersAndNotTheHosts(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define job (spawn-detached (lambda () (sleep-ms 80) (boom))))`)
	if got := scheme.Write(e.ok(t, `(guard (x (#t 'caught)) (task-wait job))`)); got != "caught" {
		t.Errorf("got %s", got)
	}
	select {
	case f := <-e.failed:
		t.Errorf("the host was told of a failure that was waited for: %v", f.err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestADenialInADetachedTaskIsTheHostsToHear(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.in.SetGate(denyBoom{})
	e.ok(t, `(spawn-detached (lambda () (sleep-ms 20) (boom)))`)
	select {
	case f := <-e.failed:
		var d *scheme.Denied
		if !errors.As(f.err, &d) {
			t.Errorf("hook error = %T %v, want a denial", f.err, f.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host was never told")
	}
	e.ok(t, `(+ 1 1)`)
}

type denyBoom struct{}

func (denyBoom) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if p.Name == "boom" {
		return &scheme.Denied{Prim: p.Name, Reason: "no"}
	}
	return nil
}
func (denyBoom) RegisterChecker(name, verdict string, predicate scheme.Value) error { return nil }

func TestARunningDetachedTaskStillKeepsTheSpawnersOrigin(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	var got atomic.Value
	if err := e.in.Register(&scheme.Prim{Name: "who", Meta: scheme.Meta{Doc: "origin"}, Handler: func(c *scheme.Call) (scheme.Value, error) {
		got.Store(string(c.Origin))
		return scheme.Void, nil
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := scheme.WithOrigin(context.Background(), scheme.OriginModel)
	if _, err := e.evalCtx(ctx, `(task-wait (spawn-detached (lambda () (who))))`); err != nil {
		t.Fatal(err)
	}
	if got.Load() != string(scheme.OriginModel) {
		t.Errorf("origin in the detached task = %v", got.Load())
	}
}

// 3. A channel the host feeds from outside.
func TestAHostFedChannelIsNotADeadlock(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	inbox, err := scheme.NewChannel(e.in, 4, scheme.HostFed())
	if err != nil {
		t.Fatal(err)
	}
	e.in.Global().Define("inbox", inbox.Value())

	result := make(chan error, 1)
	var got atomic.Value
	go func() {
		v, err := e.in.EvalString(context.Background(), `(channel-recv inbox)`)
		got.Store(scheme.Write(v))
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("the wait ended before the host sent anything: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	sent, err := inbox.TrySend(scheme.Int(42))
	if !sent || err != nil {
		t.Fatalf("TrySend = %v, %v", sent, err)
	}
	select {
	case err := <-result:
		if err != nil || got.Load() != "42" {
			t.Errorf("got %v, %v", got.Load(), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait never ended")
	}
}

func TestAChannelThatIsNotHostFedIsStillADeadlock(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	inbox, _ := scheme.NewChannel(e.in, 4)
	e.in.Global().Define("inbox", inbox.Value())
	e.fails(t, `(channel-recv inbox)`, "deadlock")
	// Marked afterwards, it is not.
	inbox.SetHostFed(true)
	go func() {
		time.Sleep(100 * time.Millisecond)
		inbox.TrySend(scheme.Str("hi"))
	}()
	if got := scheme.Write(e.ok(t, `(channel-recv inbox)`)); got != `"hi"` {
		t.Errorf("got %s", got)
	}
}

func TestHostFedChannelsWorkInSelectAndInTasks(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	inbox, _ := scheme.NewChannel(e.in, 4, scheme.HostFed())
	e.in.Global().Define("inbox", inbox.Value())
	// A detached task serves the host's messages; the evaluation that made it
	// is long over.
	e.ok(t, `(define never (make-channel))
(define server (spawn-detached (lambda ()
  (let loop ((n 0))
    (let ((x (cdr (select (list (on-recv inbox) (on-recv never))))))
      (if (= x 0) n (loop (+ n x))))))))`)
	for _, n := range []int64{1, 2, 3, 4, 0} {
		for {
			sent, err := inbox.TrySend(scheme.Int(n))
			if err != nil {
				t.Fatal(err)
			}
			if sent {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	if got := scheme.Write(e.ok(t, `(task-wait server)`)); got != "10" {
		t.Errorf("sum = %s", got)
	}
}

// A host waiting on a channel is not a task: it neither causes nor hides a
// script's deadlock.
func TestTheHostWaitingDoesNotHideAScriptsDeadlock(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	idle, _ := scheme.NewChannel(e.in, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := idle.Recv(ctx); done <- err }()
	time.Sleep(50 * time.Millisecond)
	e.fails(t, `(channel-recv (make-channel))`, "deadlock")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Recv = %v", err)
	}
}

// 3 (continued). A timer that survives its evaluation.
func TestADetachedTimerFiresForALaterEvaluation(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define t (after-detached 0.15))
(define plain (after 0.15))`)
	if got := scheme.Write(e.ok(t, `(channel-try-recv t 'none)`)); got != "none" {
		t.Fatalf("fired early: %s", got)
	}
	time.Sleep(400 * time.Millisecond)
	if got := scheme.Write(e.ok(t, `(channel-try-recv t 'none)`)); got != "#t" {
		t.Errorf("the detached timer = %s", got)
	}
	// The ordinary one is the evaluation's, as documented.
	if got := scheme.Write(e.ok(t, `(channel-try-recv plain 'none)`)); got != "none" {
		t.Errorf("the ordinary timer = %s; after ends with its evaluation", got)
	}
	// Waiting for one in a later evaluation is not a deadlock either.
	e.ok(t, `(define t2 (after-detached 0.1))`)
	start := time.Now()
	if got := scheme.Write(e.ok(t, `(channel-recv t2)`)); got != "#t" || time.Since(start) < 50*time.Millisecond {
		t.Errorf("recv = %s after %v", got, time.Since(start))
	}
	// Once it has fired it is an ordinary channel again.
	e.fails(t, `(channel-recv t2)`, "deadlock")
}

func TestADetachedTaskCanSelectOnADetachedTimer(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	e.ok(t, `(define never (make-channel))
(define job (spawn-detached (lambda ()
  (car (select (list (on-recv never) (on-recv (after-detached 0.1))))))))`)
	if got := scheme.Write(e.ok(t, `(task-wait job)`)); got != "1" {
		t.Errorf("got %s", got)
	}
}

// 2. The Go-level API, from several goroutines.
func TestChannelFromManyGoroutines(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	// The script consumer below waits on it while only the host's goroutines
	// send, which is what HostFed is for.
	ch, err := scheme.NewChannel(e.in, 8, scheme.HostFed())
	if err != nil {
		t.Fatal(err)
	}
	const producers, per = 6, 400
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				for {
					sent, err := ch.TrySend(scheme.Int(1))
					if err != nil {
						t.Error(err)
						return
					}
					if sent {
						break
					}
					time.Sleep(10 * time.Microsecond)
				}
			}
		}()
	}
	var total atomic.Int64
	var consumers sync.WaitGroup
	// Three kinds of consumer at once: polling, blocking, and a script.
	consumers.Add(3)
	go func() {
		defer consumers.Done()
		for {
			v, ok, closed := ch.TryRecv()
			switch {
			case ok:
				total.Add(v.I)
			case closed:
				return
			default:
				time.Sleep(10 * time.Microsecond)
			}
		}
	}()
	go func() {
		defer consumers.Done()
		for {
			v, ok, err := ch.Recv(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			if !ok {
				return
			}
			total.Add(v.I)
		}
	}()
	e.in.Global().Define("ch", ch.Value())
	go func() {
		defer consumers.Done()
		v, err := e.in.EvalString(context.Background(), `(let loop ((n 0)) (let ((x (channel-recv ch 'eof))) (if (eq? x 'eof) n (loop (+ n x)))))`)
		if err != nil {
			t.Error(err)
			return
		}
		total.Add(v.I)
	}()
	wg.Wait()
	for ch.Closed() == false {
		if v, ok, _ := ch.TryRecv(); ok {
			total.Add(v.I)
		} else {
			// Producers are done and the buffer is empty once nothing is ready.
			ch.Close()
		}
	}
	consumers.Wait()
	if got := total.Load(); got != producers*per {
		t.Errorf("received %d values, sent %d", got, producers*per)
	}
}

func TestChannelContract(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	ch, _ := scheme.NewChannel(e.in, 1)
	if _, ok, closed := ch.TryRecv(); ok || closed {
		t.Error("an empty open channel reported something")
	}
	if sent, err := ch.TrySend(scheme.Str("a")); !sent || err != nil {
		t.Fatalf("TrySend = %v %v", sent, err)
	}
	if sent, err := ch.TrySend(scheme.Str("b")); sent || err != nil {
		t.Errorf("TrySend on a full channel = %v %v", sent, err)
	}
	ch.Close()
	ch.Close() // idempotent
	if !ch.Closed() {
		t.Error("not closed")
	}
	if sent, err := ch.TrySend(scheme.Str("c")); sent || err == nil {
		t.Errorf("TrySend on a closed channel = %v %v", sent, err)
	}
	// What it holds can still be received, then it reports closed.
	if v, ok, closed := ch.TryRecv(); !ok || closed || v.S != "a" {
		t.Errorf("TryRecv = %v %v %v", v, ok, closed)
	}
	if _, ok, closed := ch.TryRecv(); ok || !closed {
		t.Errorf("TryRecv of a drained closed channel: ok=%v closed=%v", ok, closed)
	}
	if _, ok, err := ch.Recv(context.Background()); ok || err != nil {
		t.Errorf("Recv of a drained closed channel: ok=%v err=%v", ok, err)
	}

	// A script's channel is reachable from its value, a non-channel is not.
	v := e.ok(t, `(make-channel 2)`)
	sc, ok := scheme.AsChannel(v)
	if !ok {
		t.Fatal("AsChannel on a channel")
	}
	if _, ok := scheme.AsChannel(scheme.Int(1)); ok {
		t.Error("AsChannel on an integer")
	}
	e.in.Global().Define("c", v)
	sc.TrySend(scheme.Int(7))
	if got := scheme.Write(e.ok(t, `(channel-recv c)`)); got != "7" {
		t.Errorf("script received %s", got)
	}
	e.ok(t, `(channel-send! c 8)`)
	if v, ok, _ := sc.TryRecv(); !ok || v.I != 8 {
		t.Errorf("host received %v %v", v, ok)
	}
	// Taint crosses the Go door unchanged: a channel does not launder.
	sc.TrySend(scheme.Tainted(scheme.Str("x"), scheme.TaintOf(scheme.TagFile)))
	if got := e.ok(t, `(channel-recv c)`); got.Taint.IsClean() {
		t.Error("the value lost its taint passing through a channel from the host")
	}

	if _, err := scheme.NewChannel(e.in, -1); err == nil {
		t.Error("negative capacity accepted")
	}
	if _, err := scheme.NewChannel(scheme.New(), 1); err == nil {
		t.Error("a channel for an interpreter with no tasks")
	}

	// Recv stops with its context.
	idle, _ := scheme.NewChannel(e.in, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := idle.Recv(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Recv = %v", err)
	}
	// A receiver parked on an unbuffered channel is satisfied by TrySend.
	rv, _ := scheme.NewChannel(e.in, 0)
	got := make(chan scheme.Value, 1)
	go func() { v, _, _ := rv.Recv(context.Background()); got <- v }()
	waitFor(t, "the host's receive to be accepted", func() bool {
		sent, _ := rv.TrySend(scheme.Int(5))
		return sent
	})
	if v := <-got; v.I != 5 {
		t.Errorf("got %v", v)
	}
}

func TestTaskFromManyGoroutines(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	v := e.ok(t, `(spawn-detached (lambda () (sleep-ms 100) 'result))`)
	tk, ok := scheme.AsTask(v)
	if !ok {
		t.Fatal("AsTask on a task")
	}
	if _, ok := scheme.AsTask(scheme.Int(1)); ok {
		t.Error("AsTask on an integer")
	}
	if _, err := tk.Result(); !errors.Is(err, scheme.ErrTaskRunning) {
		t.Errorf("Result before Done = %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-tk.Done():
			case <-time.After(5 * time.Second):
				t.Error("Done never closed")
				return
			}
			v, err := tk.Result()
			if err != nil || scheme.Write(v) != "result" {
				t.Errorf("Result = %s, %v", scheme.Write(v), err)
			}
			tk.Cancel() // harmless on a finished task
		}()
	}
	wg.Wait()
	// The script can use the handle it got back from the host.
	e.in.Global().Define("t", tk.Value())
	if got := scheme.Write(e.ok(t, `(task-wait t)`)); got != "result" {
		t.Errorf("got %s", got)
	}

	// Cancel from Go, and Result says it was a cancellation.
	hv := e.ok(t, `(spawn-detached (lambda () (hold)))`)
	ht, _ := scheme.AsTask(hv)
	waitFor(t, "hold", func() bool { return e.holding.Load() == 1 })
	ht.Cancel()
	<-ht.Done()
	if _, err := ht.Result(); !errors.Is(err, context.Canceled) {
		t.Errorf("Result of a cancelled task = %v", err)
	}
}

func TestTheTaskThatStartedAnEvaluationIsDoneWithIt(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	v := e.ok(t, `(current-task)`)
	tk, _ := scheme.AsTask(v)
	select {
	case <-tk.Done():
	case <-time.After(time.Second):
		t.Fatal("the main task of a finished evaluation is not done")
	}
}

// 4. The context value the host put in an evaluation is visible beneath it,
// in every kind of task.
func TestTheHostsContextValueIsVisibleInEveryKindOfTask(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{})
	defer shutdown(t, e.in)
	cases := map[string]string{
		"the evaluation":       `(ctx-tag)`,
		"spawn":                `(task-wait (spawn (lambda () (ctx-tag))))`,
		"nested spawn":         `(task-wait (spawn (lambda () (task-wait (spawn (lambda () (ctx-tag)))))))`,
		"group-spawn":          `(task-group (lambda (g) (task-wait (group-spawn g (lambda () (ctx-tag))))))`,
		"pmap":                 `(car (pmap (lambda (x) (ctx-tag)) '(1 2 3)))`,
		"pmap-limit":           `(car (pmap-limit 2 (lambda (x) (ctx-tag)) '(1 2 3)))`,
		"pfor-each":            `(let ((a (make-atom '()))) (pfor-each (lambda (x) (swap! a (lambda (l) (cons (ctx-tag) l)))) '(1 2)) (car (atom-ref a)))`,
		"race":                 `(race (list (lambda () (ctx-tag)) (lambda () (sleep-ms 2000))))`,
		"with-timeout":         `(with-timeout 5 (lambda () (ctx-tag)))`,
		"pmap inside spawn":    `(task-wait (spawn (lambda () (car (pmap (lambda (x) (ctx-tag)) '(1))))))`,
		"timeout inside pmap":  `(car (pmap (lambda (x) (with-timeout 5 (lambda () (ctx-tag)))) '(1)))`,
		"a callback":           `(car (map (lambda (x) (ctx-tag)) '(1)))`,
		"a sort comparator":    `(let ((seen '())) (sort '(2 1) (lambda (a b) (set! seen (ctx-tag)) (< a b))) seen)`,
		"with-lock":            `(with-lock (make-mutex) (lambda () (ctx-tag)))`,
		"swap!":                `(let ((a (make-atom "")))  (swap! a (lambda (x) (ctx-tag))))`,
		"detached task":        `(task-wait (spawn-detached (lambda () (ctx-tag))))`,
		"detached then spawn":  `(task-wait (spawn-detached (lambda () (task-wait (spawn (lambda () (ctx-tag)))))))`,
		"detached then pmap":   `(task-wait (spawn-detached (lambda () (car (pmap (lambda (x) (ctx-tag)) '(1))))))`,
		"detached in a worker": `(task-wait (car (pmap (lambda (x) (spawn-detached (lambda () (ctx-tag)))) '(1))))`,
	}
	ctx := context.WithValue(context.Background(), tagKey, "module-a")
	for name, src := range cases {
		v, err := e.evalCtx(ctx, src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if v.S != "module-a" {
			t.Errorf("%s: saw %q, want %q", name, v.S, "module-a")
		}
	}
}

// A detached task keeps the value of the evaluation that started it, not that
// of whichever later evaluation waits for it; and evaluations at the same
// time with different values never see each other's.
func TestContextValuesDoNotCrossBetweenEvaluations(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{MaxTasks: 512})
	defer shutdown(t, e.in)
	ctxA := context.WithValue(context.Background(), tagKey, "A")
	ctxB := context.WithValue(context.Background(), tagKey, "B")
	if _, err := e.evalCtx(ctxA, `(define job (spawn-detached (lambda () (sleep-ms 50) (ctx-tag))))`); err != nil {
		t.Fatal(err)
	}
	v, err := e.evalCtx(ctxB, `(list (task-wait job) (ctx-tag))`)
	if err != nil {
		t.Fatal(err)
	}
	if got := scheme.Write(v); got != `("A" "B")` {
		t.Errorf("got %s", got)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		tag := fmt.Sprintf("m%d", g)
		ctx := context.WithValue(context.Background(), tagKey, tag)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				v, err := e.evalCtx(ctx, `(append (pmap (lambda (x) (ctx-tag)) '(1 2 3)) (list (task-wait (spawn-detached (lambda () (ctx-tag))))))`)
				if err != nil {
					t.Error(err)
					return
				}
				items, _ := v.Slice()
				for _, it := range items {
					if it.S != tag {
						t.Errorf("%s saw %q", tag, it.S)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// 6. Applying a closure on one goroutine while another evaluation runs, with
// definitions, snapshots and restores of a module's environment going on,
// and detached tasks running.
func TestClosuresAndModuleEnvironmentsAreSafeWhileOtherEvaluationsRun(t *testing.T) {
	e := newBgInterp(t, scheme.TaskLimits{MaxTasks: 64})
	defer shutdown(t, e.in)
	mod := scheme.NewEnv(e.in.Global())
	ctx := context.Background()
	forms, err := scheme.Read(`
(define (bump n) (+ n 1))
(define counter (make-atom 0))
(define (work) (swap! counter bump))`)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range forms {
		if _, err := e.in.Eval(ctx, f, mod); err != nil {
			t.Fatal(err)
		}
	}
	loop, _ := scheme.ReadOne(`(spawn-detached (lambda () (let loop ((i 0)) (when (< i 3000) (work) (loop (+ i 1))))))`)
	var jobs []scheme.Task
	for i := 0; i < 3; i++ {
		v, err := e.in.Eval(ctx, loop, mod)
		if err != nil {
			t.Fatal(err)
		}
		tk, _ := scheme.AsTask(v)
		jobs = append(jobs, tk)
	}
	bump, _ := mod.Lookup("bump")
	work, _ := mod.Lookup("work")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	run := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					f(i)
				}
			}
		}()
	}
	run(func(i int) { // another goroutine applies the module's closures
		if v, err := e.in.Apply(ctx, bump, []scheme.Value{scheme.Int(int64(i))}); err != nil || v.I != int64(i)+1 {
			t.Errorf("Apply bump: %v %v", v, err)
		}
		if _, err := e.in.Apply(ctx, work, nil); err != nil {
			t.Errorf("Apply work: %v", err)
		}
	})
	run(func(i int) { // an editor "transaction" on the module's environment
		snap := mod.Snapshot()
		mod.Define("scratch", scheme.Int(int64(i)))
		mod.Define(fmt.Sprintf("tmp%d", i%5), scheme.Void)
		mod.Restore(snap)
		if _, ok := mod.Lookup("bump"); !ok {
			t.Error("bump vanished from the module")
		}
	})
	run(func(i int) { // and a third evaluation, with tasks of its own
		v, err := e.in.EvalString(ctx, `(task-wait-all (map (lambda (k) (spawn (lambda () (+ k 1)))) (iota 4)))`)
		if err != nil || scheme.Write(v) != "(1 2 3 4)" {
			t.Errorf("evaluation: %s %v", scheme.Write(v), err)
		}
	})
	for _, j := range jobs {
		select {
		case <-j.Done():
		case <-time.After(30 * time.Second):
			t.Fatal("a detached task never finished")
		}
		if _, err := j.Result(); err != nil {
			t.Errorf("detached task: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	// Snapshot and Restore lost none of the work: the atom is shared, not
	// copied, and 3 tasks bumped it 3000 times each.
	read, _ := scheme.ReadOne(`(atom-ref counter)`)
	v, err := e.in.Eval(ctx, read, mod)
	if err != nil || v.I < 9000 {
		t.Errorf("counter = %s, %v", scheme.Write(v), err)
	}
}

// 6. A gate that says it is concurrent is called without the interpreter's
// lock: two calls are inside it at the same moment. One that does not say so
// is never called twice at once.
func TestAConcurrentGateIsCalledWithoutTheInterpretersLock(t *testing.T) {
	probe := func(g scheme.Gate) (maxInside int64) {
		in := scheme.New(scheme.WithTasks(scheme.TaskLimits{}))
		if err := in.Register(&scheme.Prim{Name: "gate-probe", Meta: scheme.Meta{Doc: "probe"}, Handler: func(c *scheme.Call) (scheme.Value, error) { return scheme.Void, nil }}); err != nil {
			t.Fatal(err)
		}
		in.SetGate(g)
		if _, err := in.EvalString(context.Background(), `(task-wait-all (list (spawn (lambda () (gate-probe))) (spawn (lambda () (gate-probe)))))`); err != nil {
			t.Fatal(err)
		}
		switch g := g.(type) {
		case *probeGate:
			return g.max.Load()
		case *concurrentProbeGate:
			return g.max.Load()
		}
		return -1
	}
	if got := probe(&concurrentProbeGate{probeGate{wait: 2 * time.Second}}); got != 2 {
		t.Errorf("a ConcurrentGate never had two calls inside it at once (max %d)", got)
	}
	if got := probe(&probeGate{wait: 100 * time.Millisecond}); got != 1 {
		t.Errorf("a plain Gate was called concurrently (max inside %d)", got)
	}
}

type probeGate struct {
	inside, max atomic.Int64
	wait        time.Duration
}

func (g *probeGate) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if p.Name != "gate-probe" {
		return nil
	}
	n := g.inside.Add(1)
	defer g.inside.Add(-1)
	for {
		m := g.max.Load()
		if n <= m || g.max.CompareAndSwap(m, n) {
			break
		}
	}
	deadline := time.Now().Add(g.wait)
	for g.inside.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return nil
}
func (g *probeGate) RegisterChecker(name, verdict string, predicate scheme.Value) error { return nil }

type concurrentProbeGate struct{ probeGate }

func (g *concurrentProbeGate) ConcurrentSafe() {}

// The pattern the migration note in docs/embedding.md describes: the host owns
// an event loop; scripts start detached work and name a handler; the loop
// turns each task finishing, each message on a host-fed channel and each
// failure into a callback that runs as a new, short command on the UI
// goroutine. The UI goroutine never blocks on Scheme.
type eventLoop struct {
	in     *scheme.Interp
	events chan func()   // run on the UI goroutine, one at a time
	quit   chan struct{} // closed by Close
	wg     sync.WaitGroup
	errs   []error // touched only on the UI goroutine
}

func newEventLoop(t *testing.T) *eventLoop {
	l := &eventLoop{events: make(chan func(), 64), quit: make(chan struct{})}
	l.in = scheme.New(
		scheme.WithTasks(scheme.TaskLimits{Background: true, MaxTasks: 32}),
		scheme.WithTaskFailed(func(tk scheme.Task, err error) {
			l.post(func() { l.errs = append(l.errs, err) })
		}),
	)
	// (on-done task handler): when the task finishes, call (handler result).
	// (on-message channel handler): call (handler value) for each message.
	reg := func(name string, h func(c *scheme.Call) (scheme.Value, error)) {
		err := l.in.Register(&scheme.Prim{Name: name, Meta: scheme.Meta{Doc: name, Params: []scheme.Param{{Name: "source", Type: "any"}, {Name: "handler", Type: "any"}}}, Handler: h})
		if err != nil {
			t.Fatal(err)
		}
	}
	reg("on-done", func(c *scheme.Call) (scheme.Value, error) {
		tk, ok := scheme.AsTask(c.Args[0])
		if !ok {
			return scheme.Value{}, errors.New("on-done: expected a task")
		}
		l.onDone(tk, c.Args[1])
		return scheme.Void, nil
	})
	reg("on-message", func(c *scheme.Call) (scheme.Value, error) {
		ch, ok := scheme.AsChannel(c.Args[0])
		if !ok {
			return scheme.Value{}, errors.New("on-message: expected a channel")
		}
		l.onMessage(ch, c.Args[1])
		return scheme.Void, nil
	})
	return l
}

// post queues f for the UI goroutine; it never blocks past Close.
func (l *eventLoop) post(f func()) {
	select {
	case l.events <- f:
	case <-l.quit:
	}
}

func (l *eventLoop) onDone(tk scheme.Task, handler scheme.Value) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		select {
		case <-tk.Done():
			v, err := tk.Result()
			if err != nil {
				return // a failure nobody waited for is the WithTaskFailed hook's
			}
			l.post(func() { l.in.Apply(context.Background(), handler, []scheme.Value{v}) })
		case <-l.quit:
		}
	}()
}

func (l *eventLoop) onMessage(ch scheme.Channel, handler scheme.Value) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { <-l.quit; cancel() }()
		for {
			v, ok, err := ch.Recv(ctx)
			if err != nil || !ok {
				return
			}
			l.post(func() { l.in.Apply(context.Background(), handler, []scheme.Value{v}) })
		}
	}()
}

// run is the UI goroutine's loop until stop returns true after a command.
func (l *eventLoop) run(stop func() bool) {
	for f := range l.events {
		f()
		if stop() {
			return
		}
	}
}

func (l *eventLoop) Close(ctx context.Context) error {
	close(l.quit)
	err := l.in.Shutdown(ctx)
	l.wg.Wait()
	return err
}

func TestTheEventLoopPatternOfTheMigrationNote(t *testing.T) {
	loop := newEventLoop(t)
	outbox, err := scheme.NewChannel(loop.in, 8, scheme.HostFed())
	if err != nil {
		t.Fatal(err)
	}
	loop.in.Global().Define("outbox", outbox.Value())
	var results []string // only the UI goroutine touches it, so no lock

	// Commands, each synchronous and short. The first starts work that goes on
	// after it returns, and names the handlers; none of them waits.
	if _, err := loop.in.EvalString(context.Background(), `
(define log '())
(define (note x) (set! log (cons x log)))
(on-done (spawn-detached (lambda () (task-wait (spawn (lambda () (* 6 7))))))
         (lambda (v) (note (list 'answer v))))
(on-message outbox (lambda (m) (note (list 'message m))))
(spawn-detached (lambda () (error "link checker crashed")))
`); err != nil {
		t.Fatal(err)
	}
	// The host, from another goroutine, feeds the channel while the command
	// is long over.
	go func() {
		for _, m := range []string{"a", "b"} {
			outbox.TrySend(scheme.Str(m))
		}
	}()

	loop.run(func() bool {
		v, _ := loop.in.EvalString(context.Background(), `(length log)`)
		return v.I == 3 && len(loop.errs) == 1
	})
	v, _ := loop.in.EvalString(context.Background(), `(map (lambda (x) (car x)) log)`)
	results = append(results, scheme.Write(v))
	if !strings.Contains(results[0], "answer") || strings.Count(results[0], "message") != 2 {
		t.Errorf("handlers ran as %s", results[0])
	}
	if got := loop.errs[0].Error(); !strings.Contains(got, "link checker crashed") {
		t.Errorf("hook error = %s", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := loop.Close(ctx); err != nil {
		t.Error(err)
	}
}
