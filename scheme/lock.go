package scheme

import (
	"fmt"
	"sync/atomic"
)

// mutexObj is a lock tasks queue for. Waiting tasks park like any other, so a
// lock taken twice by one task, or in a cycle, is reported as a deadlock.
type mutexObj struct {
	rt      *taskRuntime
	id      int64
	locked  bool      // under rt.mu
	waiters []*waiter // under rt.mu
}

func (m *mutexObj) Kind() string   { return "mutex" }
func (m *mutexObj) String() string { return fmt.Sprintf("#<mutex %d>", m.id) }

// lock takes the mutex, waiting for it if another task has it.
func (m *mutexObj) lock(c *Call) error {
	rt := m.rt
	rt.mu.Lock()
	if !m.locked {
		m.locked = true
		rt.mu.Unlock()
		return nil
	}
	w := newWaiter(c.t)
	m.waiters = append(m.waiters, w)
	rt.park(c.Ctx, w)
	rt.mu.Lock()
	for i, x := range m.waiters {
		if x == w {
			m.waiters = append(m.waiters[:i], m.waiters[i+1:]...)
			break
		}
	}
	rt.mu.Unlock()
	// Unlock hands the lock to the waiter it fires, so a fired waiter has it.
	if w.err != nil {
		return ctxOrDeadlock(c, w.err)
	}
	return nil
}

// unlock gives the mutex to the longest waiting task, or frees it.
func (m *mutexObj) unlock() {
	rt := m.rt
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for len(m.waiters) > 0 {
		w := m.waiters[0]
		m.waiters = m.waiters[1:]
		if !w.done {
			rt.fire(w, 0, Value{}, true, nil)
			return
		}
	}
	m.locked = false
}

// withLock runs thunk holding the mutex and releases it however thunk ends.
func (in *Interp) withLock(c *Call, m *mutexObj, thunk Value) (Value, error) {
	if err := m.lock(c); err != nil {
		return Value{}, err
	}
	defer m.unlock()
	return in.apply(c.t, c.Ctx, thunk, nil)
}

// atomObj is a cell with atomic reads and writes. The value is replaced as a
// whole, so a reader never sees half of one.
type atomObj struct {
	id int64
	p  atomic.Pointer[Value]
}

func (a *atomObj) Kind() string   { return "atom" }
func (a *atomObj) String() string { return fmt.Sprintf("#<atom %d>", a.id) }

// swap replaces the value with f of it: f runs with nothing held, and its
// result is stored only if no one stored since, else it runs again on the
// new value. f may therefore run more than once.
func (a *atomObj) swap(c *Call, f Value) (Value, error) {
	for {
		old := a.p.Load()
		nv, err := c.In.apply(c.t, c.Ctx, f, []Value{*old})
		if err != nil {
			return Value{}, err
		}
		if a.p.CompareAndSwap(old, &nv) {
			return nv, nil
		}
		if err := c.Ctx.Err(); err != nil {
			return Value{}, wrap(c.Name, err)
		}
	}
}

// onceObj runs a procedure once however many tasks ask.
type onceObj struct {
	rt      *taskRuntime
	id      int64
	state   int // under rt.mu: 0 not started, 1 running, 2 done
	val     Value
	waiters []*waiter
}

func (o *onceObj) Kind() string   { return "once" }
func (o *onceObj) String() string { return fmt.Sprintf("#<once %d>", o.id) }

// do runs thunk if no one has and returns its value; a task that asks while
// another is running it waits for that one. If thunk fails nothing is
// remembered: the next task to ask runs it again.
func (in *Interp) onceDo(c *Call, o *onceObj, thunk Value) (Value, error) {
	rt := o.rt
	for {
		rt.mu.Lock()
		switch o.state {
		case 2:
			v := o.val
			rt.mu.Unlock()
			return v, nil
		case 0:
			o.state = 1
			rt.mu.Unlock()
			v, err := in.apply(c.t, c.Ctx, thunk, nil)
			rt.mu.Lock()
			if err == nil {
				o.state, o.val = 2, v
			} else {
				o.state = 0
			}
			for _, w := range o.waiters {
				if !w.done {
					rt.fire(w, 0, Value{}, true, nil)
				}
			}
			o.waiters = nil
			rt.mu.Unlock()
			return v, err
		}
		w := newWaiter(c.t)
		o.waiters = append(o.waiters, w)
		rt.park(c.Ctx, w)
		if w.err != nil {
			return Value{}, ctxOrDeadlock(c, w.err)
		}
	}
}
