package scheme

import (
	"context"
	"time"
)

// This file is the one place a task waits for another: joins, channels,
// select, timers and (later) locks all park here. They share one mutex per
// interpreter so that "this task is about to wait" and "that task is about to
// wake it" are decided together; that is what lets the interpreter say, exactly
// and without a timeout, that every task is waiting on one another and none
// can ever run (a deadlock).
//
// The lock is held for a queue operation and never across a call into Scheme,
// a handler, or a wait.

// errDeadlock is what the task that started an evaluation is woken with when
// no task of the interpreter can ever run again.
func errDeadlock() error {
	return errorf("deadlock", "all tasks are waiting for one another and none can run")
}

// waiter is one parked task. Whoever wakes it fills in the outcome first.
type waiter struct {
	task *task
	wake chan struct{} // buffered 1; receives when the waiter is fired

	// Under rt.mu:
	done bool // fired or abandoned; nobody may fire it again
	idx  int  // which of its cases fired
	val  Value
	ok   bool
	err  error

	cases []*wcase // the channel queues it is on, to leave them afterwards

	// counted: it is in rt.blocked and rt.parked. A waiter that the outside
	// world may wake (a host receiving on a channel, a task waiting on a
	// host-fed channel) is not, so it never makes the others look deadlocked.
	counted bool
}

func newWaiter(t *task) *waiter {
	return &waiter{task: t, wake: make(chan struct{}, 1)}
}

// wcase is a waiter's place in one channel's queue.
type wcase struct {
	w    *waiter
	idx  int
	ch   *channelObj
	send bool
	val  Value // the value a sender offers
}

// fire completes w with an outcome and wakes it. rt.mu is held.
func (rt *taskRuntime) fire(w *waiter, idx int, val Value, ok bool, err error) {
	w.done, w.idx, w.val, w.ok, w.err = true, idx, val, ok, err
	if w.counted {
		rt.blocked--
		delete(rt.parked, w)
	}
	w.wake <- struct{}{}
}

// hostFed reports whether w waits on a channel the host feeds from outside.
func (w *waiter) hostFed() bool {
	for _, c := range w.cases {
		if c.ch.hostFed {
			return true
		}
	}
	return false
}

// park blocks the calling task until w is fired or ctx is done. The caller
// holds rt.mu with w queued wherever it should be found; park releases it.
// It returns with the lock released, w's outcome set, and w no longer queued.
func (rt *taskRuntime) park(ctx context.Context, w *waiter) {
	w.counted = w.task != nil && !w.hostFed()
	if w.counted {
		rt.blocked++
		rt.parked[w] = struct{}{}
		rt.checkDeadlock()
	}
	rt.mu.Unlock()
	select {
	case <-w.wake:
		rt.mu.Lock()
	case <-ctx.Done():
		rt.mu.Lock()
		if !w.done {
			// Abandoned before anyone fired it.
			w.done, w.err = true, ctx.Err()
			if w.counted {
				rt.blocked--
				delete(rt.parked, w)
			}
		}
	}
	for _, c := range w.cases {
		c.ch.dequeue(c)
	}
	rt.mu.Unlock()
}

// checkDeadlock wakes the task that started each evaluation with a deadlock
// error if every task of the interpreter is parked and nothing, such as a
// timer, can wake any of them. rt.mu is held.
//
// A task in a host call (a request, a program, a sleep) is not parked: it will
// come back and may wake the others, so it is not a deadlock.
func (rt *taskRuntime) checkDeadlock() {
	if rt.alive == 0 || rt.blocked != rt.alive || rt.timers != 0 {
		return
	}
	for w := range rt.parked {
		if w.task.isRoot() {
			rt.fire(w, 0, Value{}, false, errDeadlock())
		}
	}
}

// pendingTimer is a timer that will wake tasks, counted so it prevents a
// false deadlock while it is pending.
type pendingTimer struct {
	timer *time.Timer
	fired bool // under rt.mu: it ran, or its evaluation ended first
}

// startTimer runs fn (with rt.mu held) after d, and keeps the interpreter from
// calling a wait a deadlock until it has. The evaluation r ends it if it has
// not run by then.
func (rt *taskRuntime) startTimer(r *root, d time.Duration, fn func()) *pendingTimer {
	p := &pendingTimer{}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.timers++
	r.timers = append(r.timers, p)
	p.timer = time.AfterFunc(d, func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if p.fired {
			return
		}
		p.fired = true
		rt.timers--
		fn()
		rt.checkDeadlock()
	})
	return p
}

// stopTimer ends a timer that has not run, so it no longer counts as
// something that can wake a task.
func (rt *taskRuntime) stopTimer(p *pendingTimer) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !p.fired {
		p.fired = true
		p.timer.Stop()
		rt.timers--
	}
}

// stopTimers ends the evaluation's timers that have not run. rt.mu is held.
func (r *root) stopTimers() {
	for _, p := range r.timers {
		if !p.fired {
			p.fired = true
			p.timer.Stop()
			r.rt.timers--
		}
	}
	r.timers = nil
}
