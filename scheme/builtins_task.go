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
	in.registerChannels()
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
