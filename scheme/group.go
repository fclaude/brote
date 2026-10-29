package scheme

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// groupObj is a set of tasks that live and die together: the first to fail
// cancels the rest (like Go's errgroup), and the group is waited for as a
// whole.
type groupObj struct {
	rt     *taskRuntime
	id     int64
	ctx    context.Context
	cancel context.CancelCauseFunc

	mu     sync.Mutex
	first  error // the first failure of a task that was not cancelled
	closed bool  // under rt.mu: group-spawn is refused

	pending int       // under rt.mu: tasks not yet finished
	waiters []*waiter // under rt.mu
}

func (g *groupObj) Kind() string   { return "task group" }
func (g *groupObj) String() string { return fmt.Sprintf("#<task-group %d>", g.id) }

func (rt *taskRuntime) newGroup(parent context.Context) *groupObj {
	ctx, cancel := context.WithCancelCause(parent)
	return &groupObj{rt: rt, id: rt.ids.Add(1), ctx: ctx, cancel: cancel}
}

// fail records a task's failure and cancels the rest of the group.
func (g *groupObj) fail(err error) {
	g.mu.Lock()
	if g.first == nil {
		g.first = err
	}
	g.mu.Unlock()
	g.cancel(err)
}

func (g *groupObj) failure() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.first
}

// spawn starts body in the group.
func (g *groupObj) spawn(in *Interp, c *Call, body taskBody) (*taskObj, error) {
	g.rt.mu.Lock()
	closed := g.closed
	g.rt.mu.Unlock()
	if closed {
		return nil, errorf(c.Name, "the task group has ended")
	}
	return in.startTask(c, g.ctx, g, body)
}

// wait blocks until every task of the group has finished, or ctx is done, or
// nothing can run.
func (g *groupObj) wait(c *Call, ctx context.Context) error {
	rt := g.rt
	rt.mu.Lock()
	if g.pending == 0 {
		rt.mu.Unlock()
		return nil
	}
	w := newWaiter(c.t)
	g.waiters = append(g.waiters, w)
	rt.park(ctx, w)
	rt.mu.Lock()
	for i, x := range g.waiters {
		if x == w {
			g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)
			break
		}
	}
	rt.mu.Unlock()
	if w.err != nil {
		return ctxOrDeadlock(c, w.err)
	}
	return nil
}

// finish ends the group: the tasks still running are cancelled if cancelNow,
// and waited for either way. It returns the failure the group ends with, if any.
func (g *groupObj) finish(c *Call, cancelNow error) error {
	if cancelNow != nil {
		g.cancel(cancelNow)
	}
	if err := g.wait(c, c.Ctx); err != nil {
		// The caller was cancelled or the group is deadlocked. Stop the
		// tasks, and wait for them to unwind however long that takes.
		g.cancel(err)
		g.wait(c, context.WithoutCancel(c.Ctx))
		if g.failure() == nil {
			g.rt.closeGroup(g)
			return err
		}
	}
	g.rt.closeGroup(g)
	g.cancel(nil)
	return g.failure()
}

func (rt *taskRuntime) closeGroup(g *groupObj) {
	rt.mu.Lock()
	g.closed = true
	rt.mu.Unlock()
}

// poolMap calls f on each item, at most limit at a time, each call in a task
// of one group. It starts as many workers as the task limit leaves room for,
// up to limit, and they share the items. The first failure cancels the rest and is the error. With
// keep it returns the results in order.
func (in *Interp) poolMap(c *Call, f Value, items []Value, limit int, keep bool) (Value, error) {
	g := c.In.tasks.newGroup(c.Ctx)
	results := make([]Value, len(items))
	var next atomic.Int64
	workers := min(limit, len(items))
	var startErr error
	started := 0
	for w := 0; w < workers; w++ {
		_, err := g.spawn(in, c, func(ctx context.Context, t *task) (Value, error) {
			for {
				i := int(next.Add(1) - 1)
				if i >= len(items) || ctx.Err() != nil {
					return Void, nil
				}
				v, err := in.apply(t, ctx, f, []Value{items[i]})
				if err != nil {
					return Value{}, err
				}
				results[i] = v
			}
		})
		if err != nil {
			// Out of tasks. Workers pull their items as they go, so the
			// ones already running do all the work, only more slowly; it
			// is an error only if none could start.
			if started == 0 {
				startErr = err
			}
			break
		}
		started++
	}
	if err := g.finish(c, startErr); err != nil {
		return Value{}, err
	}
	if startErr != nil {
		return Value{}, startErr
	}
	if !keep {
		return Void, nil
	}
	return List(results...), nil
}

// race runs the thunks at once and returns the first value any returns. A
// thunk that fails only drops out of the race; the others go on.
func (in *Interp) race(c *Call, thunks []Value) (Value, error) {
	g := c.In.tasks.newGroup(c.Ctx)
	var won atomic.Bool
	var winner Value
	var mu sync.Mutex
	var firstErr error
	var startErr error
	for _, th := range thunks {
		_, err := g.spawn(in, c, func(ctx context.Context, t *task) (Value, error) {
			v, err := in.apply(t, ctx, th, nil)
			switch {
			case err == nil && won.CompareAndSwap(false, true):
				winner = v
				g.cancel(errRaceWon)
			case err != nil && ctx.Err() == nil:
				if isFatal(err) {
					return Void, err
				}
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
			return Void, nil
		})
		if err != nil {
			startErr = err
			break
		}
	}
	err := g.finish(c, startErr)
	switch {
	case won.Load():
		return winner, nil
	case err != nil:
		return Value{}, err
	case startErr != nil:
		return Value{}, startErr
	}
	mu.Lock()
	defer mu.Unlock()
	if firstErr != nil {
		return Value{}, firstErr
	}
	return Value{}, errorf(c.Name, "no task finished")
}

var errRaceWon = errors.New("another task finished first")

// errTimedOut is the cause with-timeout cancels its thunk with.
var errTimedOut = errors.New("timed out")

// withTimeout calls thunk in the calling task with a deadline. When the
// deadline passes, the thunk's context is cancelled: its waits and host calls
// end, a guard inside it cannot catch that, and the caller gets an ordinary
// "timed out" error.
func (in *Interp) withTimeout(c *Call, d time.Duration, thunk Value) (Value, error) {
	rt := in.tasks
	ctx, cancel := context.WithCancelCause(c.Ctx)
	// The deadline is one of the interpreter's timers, so a wait that it will
	// end is not reported as a deadlock.
	p := rt.startTimer(c.t.root, d, func() { cancel(errTimedOut) })
	v, err := in.apply(c.t, ctx, thunk, nil)
	rt.stopTimer(p)
	timedOut := errors.Is(context.Cause(ctx), errTimedOut) && c.Ctx.Err() == nil
	cancel(nil)
	if err != nil && timedOut {
		return Value{}, errorf(c.Name, "timed out after %s", d)
	}
	return v, err
}
