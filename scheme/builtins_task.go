package scheme

import "context"

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
	in.mustRegisterOpaque("task?", "True for tasks.", []Param{{Name: "x", Type: "any"}}, false, func(c *Call) (Value, error) {
		_, ok := ObjectOf(c.Args[0]).(*taskObj)
		return Bool(ok), nil
	})
}
