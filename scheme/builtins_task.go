package scheme

import (
	"context"
	"time"
)

func (in *Interp) registerTasks() {
	task := Param{Name: "task", Type: "task"}

	in.mustRegister("spawn", "Start a procedure of no arguments as a new task, running at the same time as the caller. Returns the task at once. Tasks end with the evaluation that started them.",
		[]Param{{Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
			thunk, err := c.Proc(0)
			if err != nil {
				return Value{}, err
			}
			return c.In.spawn(c, thunk)
		})
	in.mustRegister("task-wait", "Wait for a task to finish and return its value, or raise the error it ended with.",
		[]Param{task}, false, func(c *Call) (Value, error) {
			t, err := objArg[*taskObj](c, 0, "task")
			if err != nil {
				return Value{}, err
			}
			return t.wait(c)
		})
	in.mustRegister("task-wait-all", "Wait for every task in a list and return their values in order. If any failed, raises the error of the first that did, once all have finished.",
		[]Param{{Name: "tasks", Type: "list"}}, false, func(c *Call) (Value, error) {
			items, err := c.List(0)
			if err != nil {
				return Value{}, err
			}
			ts := make([]*taskObj, len(items))
			for i, it := range items {
				t, ok := ObjectOf(it).(*taskObj)
				if !ok {
					return Value{}, errorf(c.Name, "element %d: expected a task, got %s %s", i+1, it.Kind, Write(it))
				}
				ts[i] = t
			}
			vals := make([]Value, len(ts))
			var first error
			for i, t := range ts {
				v, err := t.wait(c)
				if err != nil {
					if c.Ctx.Err() != nil {
						return Value{}, err
					}
					if first == nil {
						first = err
					}
					continue
				}
				vals[i] = v
			}
			if first != nil {
				return Value{}, first
			}
			return List(vals...), nil
		})
	in.mustRegisterOpaque("task-done?", "True once a task has finished, by value, error or cancellation. Never waits.",
		[]Param{task}, false, func(c *Call) (Value, error) {
			t, err := objArg[*taskObj](c, 0, "task")
			if err != nil {
				return Value{}, err
			}
			return Bool(t.finished()), nil
		})
	in.mustRegister("task-cancel!", "Cancel a task: it stops at its next step, and a wait or a call it is in the middle of ends. Cancelling a finished task does nothing.",
		[]Param{task}, false, func(c *Call) (Value, error) {
			t, err := objArg[*taskObj](c, 0, "task")
			if err != nil {
				return Value{}, err
			}
			t.cancel(context.Canceled)
			return Void, nil
		})
	in.mustRegisterOpaque("current-task", "The task that is running this call.", nil, false, func(c *Call) (Value, error) {
		return ObjectValue(c.t.obj), nil
	})
	if in.tasks.limits.Background {
		in.mustRegister("spawn-detached", "Start a procedure of no arguments as a task that belongs to the interpreter, not to the evaluation: it keeps running after the evaluation returns, and a later evaluation can task-wait, task-done? or task-cancel! it. It runs under the context values of the caller (the same origin and whatever the host put there), but is not cancelled when the caller is; only task-cancel! and the host's Shutdown end it. If it fails and no one is waiting, the host is told rather than any evaluation failing. It counts against the task limit.",
			[]Param{{Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
				thunk, err := c.Proc(0)
				if err != nil {
					return Value{}, err
				}
				return c.In.spawnDetached(c, thunk)
			})
		in.mustRegister("after-detached", "Like after, but the timer belongs to the interpreter: it fires even if the evaluation that made it has returned, so a later evaluation or a detached task can wait on it. Only Shutdown cancels it (closing the channel).",
			[]Param{{Name: "seconds", Type: "number"}}, false, func(c *Call) (Value, error) {
				n, err := c.Number(0)
				if err != nil {
					return Value{}, err
				}
				secs := n.AsFloat()
				if secs < 0 || secs != secs || secs > 1e9 {
					return Value{}, errorf(c.Name, "argument 1: expected 0 to 1e9 seconds, got %s", Write(n))
				}
				ch, err := c.In.tasks.afterDetached(time.Duration(secs * float64(time.Second)))
				if err != nil {
					return Value{}, errorf(c.Name, "%v", err)
				}
				return ObjectValue(ch), nil
			})
	}
	in.registerChannels()
	in.registerGroups()
	in.registerLocks()
	in.registerPromises()
	in.mustRegisterOpaque("task?", "True for tasks.", []Param{{Name: "x", Type: "any"}}, false, func(c *Call) (Value, error) {
		_, ok := ObjectOf(c.Args[0]).(*taskObj)
		return Bool(ok), nil
	})
}

func (in *Interp) registerChannels() {
	ch := Param{Name: "ch", Type: "channel"}
	eof := Param{Name: "eof", Type: "any", Doc: "returned instead of an error when the channel is closed and empty", Optional: true}

	in.mustRegisterOpaque("make-channel", "A new channel. With no argument it is unbuffered: a send waits for a receive. With n, it holds up to n values before a send waits.",
		[]Param{{Name: "n", Type: "integer", Doc: "how many values it buffers", Optional: true}}, false, func(c *Call) (Value, error) {
			n := int64(0)
			if len(c.Args) > 0 {
				var err error
				if n, err = c.Int(0); err != nil {
					return Value{}, err
				}
			}
			if n < 0 || n > 1<<24 {
				return Value{}, errorf(c.Name, "argument 1: expected 0 to %d, got %d", 1<<24, n)
			}
			return ObjectValue(c.In.tasks.newChannel(int(n))), nil
		})
	in.mustRegisterOpaque("channel?", "True for channels.", []Param{{Name: "x", Type: "any"}}, false, func(c *Call) (Value, error) {
		_, ok := ObjectOf(c.Args[0]).(*channelObj)
		return Bool(ok), nil
	})
	in.mustRegister("channel-send!", "Send a value on a channel, waiting until a receiver or buffer room takes it. Raises an error if the channel is closed.",
		[]Param{ch, {Name: "v", Type: "any"}}, false, func(c *Call) (Value, error) {
			chn, err := objArg[*channelObj](c, 0, "channel")
			if err != nil {
				return Value{}, err
			}
			_, _, _, err = c.In.tasks.doSelect(c, []selCase{{ch: chn, send: true, val: c.Args[1]}}, true)
			return Void, err
		})
	in.mustRegister("channel-recv", "Receive a value from a channel, waiting for one. When the channel is closed and empty, raises an error, or returns eof if one was given.",
		[]Param{ch, eof}, false, func(c *Call) (Value, error) {
			chn, err := objArg[*channelObj](c, 0, "channel")
			if err != nil {
				return Value{}, err
			}
			sc := selCase{ch: chn, hasEOF: len(c.Args) > 1}
			if sc.hasEOF {
				sc.eof = c.Args[1]
			}
			_, v, _, err := c.In.tasks.doSelect(c, []selCase{sc}, true)
			return v, err
		})
	in.mustRegister("channel-try-recv", "Receive a value if one is ready, else return fallback. Never waits; a closed, empty channel also gives fallback.",
		[]Param{ch, {Name: "fallback", Type: "any"}}, false, func(c *Call) (Value, error) {
			chn, err := objArg[*channelObj](c, 0, "channel")
			if err != nil {
				return Value{}, err
			}
			idx, v, _, err := c.In.tasks.doSelect(c, []selCase{{ch: chn, hasEOF: true, eof: c.Args[1]}}, false)
			if err != nil || idx < 0 {
				return c.Args[1], err
			}
			return v, nil
		})
	in.mustRegister("channel-close!", "Close a channel: receivers still get what it holds, then find it closed, and every task waiting on it wakes. Raises an error if it is already closed.",
		[]Param{ch}, false, func(c *Call) (Value, error) {
			chn, err := objArg[*channelObj](c, 0, "channel")
			if err != nil {
				return Value{}, err
			}
			rt := c.In.tasks
			rt.mu.Lock()
			err = chn.close()
			rt.mu.Unlock()
			if err != nil {
				return Value{}, errorf(c.Name, "%v", err)
			}
			return Void, nil
		})
	in.mustRegisterOpaque("channel-closed?", "True once a channel has been closed.", []Param{ch}, false, func(c *Call) (Value, error) {
		chn, err := objArg[*channelObj](c, 0, "channel")
		if err != nil {
			return Value{}, err
		}
		chn.rt.mu.Lock()
		defer chn.rt.mu.Unlock()
		return Bool(chn.closed), nil
	})
	in.mustRegister("channel-for-each", "Call f on each value received from a channel until it is closed and empty.",
		[]Param{{Name: "f", Type: "any", Doc: "procedure of one argument"}, ch}, false, func(c *Call) (Value, error) {
			f, err := c.Proc(0)
			if err != nil {
				return Value{}, err
			}
			chn, err := objArg[*channelObj](c, 1, "channel")
			if err != nil {
				return Value{}, err
			}
			sc := selCase{ch: chn, hasEOF: true}
			for {
				_, v, ok, err := c.In.tasks.doSelect(c, []selCase{sc}, true)
				if err != nil {
					return Value{}, err
				}
				if !ok {
					return Void, nil
				}
				if _, err := c.In.Apply(c.Ctx, f, []Value{v}); err != nil {
					return Value{}, err
				}
			}
		})
	in.mustRegister("on-recv", "A select case that receives from a channel. When the channel is closed and empty, the case raises an error, or yields eof if one was given.",
		[]Param{ch, eof}, false, func(c *Call) (Value, error) {
			chn, err := objArg[*channelObj](c, 0, "channel")
			if err != nil {
				return Value{}, err
			}
			sc := selCase{ch: chn, hasEOF: len(c.Args) > 1}
			if sc.hasEOF {
				sc.eof = c.Args[1]
			}
			return ObjectValue(&selectCaseObj{sc}), nil
		})
	in.mustRegister("on-send", "A select case that sends a value on a channel.",
		[]Param{ch, {Name: "v", Type: "any"}}, false, func(c *Call) (Value, error) {
			chn, err := objArg[*channelObj](c, 0, "channel")
			if err != nil {
				return Value{}, err
			}
			return ObjectValue(&selectCaseObj{selCase{ch: chn, send: true, val: c.Args[1]}}), nil
		})
	in.mustRegister("select", "Wait until one of several channel cases can proceed and do it. cases is a list of on-recv and on-send cases. Returns (index . value): the case's position in the list, and the value received (#t for a send). When several can proceed one is picked at random. With default, a procedure of no arguments, it never waits: if no case can proceed it returns (-1 . result of default).",
		[]Param{{Name: "cases", Type: "list"}, {Name: "default", Type: "any", Doc: "procedure of no arguments", Optional: true}}, false, func(c *Call) (Value, error) {
			items, err := c.List(0)
			if err != nil {
				return Value{}, err
			}
			if len(items) == 0 && len(c.Args) < 2 {
				return Value{}, errorf(c.Name, "no cases: this would wait forever")
			}
			cases := make([]selCase, len(items))
			for i, it := range items {
				sc, ok := ObjectOf(it).(*selectCaseObj)
				if !ok {
					return Value{}, errorf(c.Name, "case %d: expected an on-recv or on-send case, got %s %s", i+1, it.Kind, Write(it))
				}
				cases[i] = sc.selCase
			}
			var def Value
			if len(c.Args) > 1 {
				if def, err = c.Proc(1); err != nil {
					return Value{}, err
				}
			}
			idx, v, _, err := c.In.tasks.doSelect(c, cases, len(c.Args) < 2)
			if err != nil {
				return Value{}, err
			}
			if idx < 0 {
				r, err := c.In.Apply(c.Ctx, def, nil)
				if err != nil {
					return Value{}, err
				}
				return Cons(Int(-1), r), nil
			}
			return Cons(Int(int64(idx)), v), nil
		})
	in.mustRegister("after", "A channel that receives #t once, after that many seconds. Cancelled with the evaluation that made it.",
		[]Param{{Name: "seconds", Type: "number"}}, false, func(c *Call) (Value, error) {
			n, err := c.Number(0)
			if err != nil {
				return Value{}, err
			}
			secs := n.AsFloat()
			if secs < 0 || secs != secs || secs > 1e9 {
				return Value{}, errorf(c.Name, "argument 1: expected 0 to 1e9 seconds, got %s", Write(n))
			}
			return ObjectValue(c.In.tasks.after(c.t.root, time.Duration(secs*float64(time.Second)))), nil
		})
}

func (in *Interp) registerGroups() {
	fn := Param{Name: "f", Type: "any", Doc: "procedure of one argument"}
	xs := Param{Name: "xs", Type: "list"}

	in.mustRegister("task-group", "Call (f group). Tasks started with group-spawn belong to the group, and task-group waits for all of them before it returns f's value. The first of them to fail cancels the rest and its error is the one raised here.",
		[]Param{{Name: "f", Type: "any", Doc: "procedure of one argument: the group"}}, false, func(c *Call) (Value, error) {
			f, err := c.Proc(0)
			if err != nil {
				return Value{}, err
			}
			g := c.In.tasks.newGroup(c.Ctx)
			v, ferr := c.In.Apply(c.Ctx, f, []Value{ObjectValue(g)})
			if gerr := g.finish(c, ferr); gerr != nil {
				return Value{}, gerr
			}
			return v, ferr
		})
	in.mustRegister("group-spawn", "Start a procedure of no arguments as a task of a group. Returns the task.",
		[]Param{{Name: "group", Type: "task group"}, {Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
			g, err := objArg[*groupObj](c, 0, "task group")
			if err != nil {
				return Value{}, err
			}
			thunk, err := c.Proc(1)
			if err != nil {
				return Value{}, err
			}
			obj, err := g.spawn(c.In, c, func(ctx context.Context, t *task) (Value, error) {
				return c.In.apply(t, ctx, thunk, nil)
			})
			if err != nil {
				return Value{}, err
			}
			return ObjectValue(obj), nil
		})
	in.mustRegister("pmap", "Like map over one list, with the calls running at the same time. Results are in order. The first call to fail cancels the others and its error is raised.",
		[]Param{fn, xs}, false, func(c *Call) (Value, error) {
			f, err := c.Proc(0)
			if err != nil {
				return Value{}, err
			}
			items, err := c.List(1)
			if err != nil {
				return Value{}, err
			}
			return c.In.poolMap(c, f, items, max(len(items), 1), true)
		})
	in.mustRegister("pmap-limit", "Like pmap, with at most n calls running at once: the form to use for a thousand URLs.",
		[]Param{{Name: "n", Type: "integer", Doc: "at most this many at once"}, fn, xs}, false, func(c *Call) (Value, error) {
			n, err := c.Int(0)
			if err != nil {
				return Value{}, err
			}
			if n < 1 {
				return Value{}, errorf(c.Name, "argument 1: expected a positive integer, got %d", n)
			}
			f, err := c.Proc(1)
			if err != nil {
				return Value{}, err
			}
			items, err := c.List(2)
			if err != nil {
				return Value{}, err
			}
			return c.In.poolMap(c, f, items, int(min(n, int64(len(items)+1))), true)
		})
	in.mustRegister("pfor-each", "Like for-each over one list, with the calls running at the same time. Yields void. The first call to fail cancels the others and its error is raised.",
		[]Param{fn, xs}, false, func(c *Call) (Value, error) {
			f, err := c.Proc(0)
			if err != nil {
				return Value{}, err
			}
			items, err := c.List(1)
			if err != nil {
				return Value{}, err
			}
			return c.In.poolMap(c, f, items, max(len(items), 1), false)
		})
	in.mustRegister("race", "Run procedures of no arguments at the same time and return the value of the first to return one; the rest are cancelled. If none returns a value, raises the first error.",
		[]Param{{Name: "thunks", Type: "list"}}, false, func(c *Call) (Value, error) {
			items, err := c.List(0)
			if err != nil {
				return Value{}, err
			}
			if len(items) == 0 {
				return Value{}, errorf(c.Name, "no procedures to run")
			}
			for i, it := range items {
				if !it.IsProcedure() {
					return Value{}, errorf(c.Name, "element %d: expected a procedure, got %s %s", i+1, it.Kind, Write(it))
				}
			}
			return c.In.race(c, items)
		})
	in.mustRegister("with-timeout", "Call a procedure of no arguments with a time limit in seconds. At the limit it is cancelled, including a request or program it is in the middle of, and the caller gets an ordinary error \"timed out\".",
		[]Param{{Name: "seconds", Type: "number"}, {Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
			n, err := c.Number(0)
			if err != nil {
				return Value{}, err
			}
			secs := n.AsFloat()
			if secs < 0 || secs != secs || secs > 1e9 {
				return Value{}, errorf(c.Name, "argument 1: expected 0 to 1e9 seconds, got %s", Write(n))
			}
			thunk, err := c.Proc(1)
			if err != nil {
				return Value{}, err
			}
			return c.In.withTimeout(c, time.Duration(secs*float64(time.Second)), thunk)
		})
}

func (in *Interp) registerLocks() {
	atom := Param{Name: "atom", Type: "atom"}

	in.mustRegisterOpaque("make-mutex", "A new lock.", nil, false, func(c *Call) (Value, error) {
		rt := c.In.tasks
		return ObjectValue(&mutexObj{rt: rt, id: rt.ids.Add(1)}), nil
	})
	in.mustRegister("with-lock", "Call a procedure of no arguments while holding a lock, waiting for it first if another task has it. The lock is released however the procedure ends, an error or a cancellation included. It is not reentrant: taking a lock you hold is a deadlock.",
		[]Param{{Name: "mutex", Type: "mutex"}, {Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
			m, err := objArg[*mutexObj](c, 0, "mutex")
			if err != nil {
				return Value{}, err
			}
			thunk, err := c.Proc(1)
			if err != nil {
				return Value{}, err
			}
			return c.In.withLock(c, m, thunk)
		})
	in.mustRegister("make-atom", "A new atom holding a value: a cell tasks can read and replace without tearing it.",
		[]Param{{Name: "v", Type: "any"}}, false, func(c *Call) (Value, error) {
			a := &atomObj{id: c.In.tasks.ids.Add(1)}
			v := c.Args[0]
			a.p.Store(&v)
			return ObjectValue(a), nil
		})
	in.mustRegister("atom-ref", "The value in an atom.", []Param{atom}, false, func(c *Call) (Value, error) {
		a, err := objArg[*atomObj](c, 0, "atom")
		if err != nil {
			return Value{}, err
		}
		return *a.p.Load(), nil
	})
	in.mustRegister("atom-set!", "Replace the value in an atom.", []Param{atom, {Name: "v", Type: "any"}}, false, func(c *Call) (Value, error) {
		a, err := objArg[*atomObj](c, 0, "atom")
		if err != nil {
			return Value{}, err
		}
		v := c.Args[1]
		a.p.Store(&v)
		return Void, nil
	})
	in.mustRegister("swap!", "Replace the value in an atom with (f value) and return it, atomically: no other update is lost. f runs with no lock held, and again on the new value if another task changed the atom meanwhile, so it may run more than once and should have no effects of its own.",
		[]Param{atom, {Name: "f", Type: "any", Doc: "procedure of one argument"}}, false, func(c *Call) (Value, error) {
			a, err := objArg[*atomObj](c, 0, "atom")
			if err != nil {
				return Value{}, err
			}
			f, err := c.Proc(1)
			if err != nil {
				return Value{}, err
			}
			return a.swap(c, f)
		})
	in.mustRegister("compare-and-set!", "Replace the value in an atom with new only if it is eqv? to old. Returns whether it did.",
		[]Param{atom, {Name: "old", Type: "any"}, {Name: "new", Type: "any"}}, false, func(c *Call) (Value, error) {
			a, err := objArg[*atomObj](c, 0, "atom")
			if err != nil {
				return Value{}, err
			}
			for {
				cur := a.p.Load()
				if !Eqv(*cur, c.Args[1]) {
					return False, nil
				}
				nv := c.Args[2]
				if a.p.CompareAndSwap(cur, &nv) {
					return True, nil
				}
			}
		})
	in.mustRegisterOpaque("make-once", "A new once: a place to run a procedure exactly once.", nil, false, func(c *Call) (Value, error) {
		rt := c.In.tasks
		return ObjectValue(&onceObj{rt: rt, id: rt.ids.Add(1)}), nil
	})
	in.mustRegister("once-do", "Call a procedure of no arguments unless a task already has, and return its value, the same for every caller. A task that asks while another is running it waits for that one. If it fails, nothing is remembered and the next caller runs it again.",
		[]Param{{Name: "once", Type: "once"}, {Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
			o, err := objArg[*onceObj](c, 0, "once")
			if err != nil {
				return Value{}, err
			}
			thunk, err := c.Proc(1)
			if err != nil {
				return Value{}, err
			}
			return c.In.onceDo(c, o, thunk)
		})
}
