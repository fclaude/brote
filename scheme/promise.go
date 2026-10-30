package scheme

import "fmt"

// promiseObj is a delayed value: a thunk that runs exactly once, on the first
// force, with every later force — from any task — returning the same value.
// It is the task model's once (lock.go) with the thunk kept alongside, so a
// promise is an atom plus a channel's worth of waiting, not a new special
// form: (delay expr) is a prelude macro that captures expr unevaluated in a
// closure and hands it to make-promise.
type promiseObj struct {
	rt    *taskRuntime
	id    int64
	once  *onceObj
	thunk Value // procedure of no arguments
}

func (p *promiseObj) Kind() string   { return "promise" }
func (p *promiseObj) String() string { return fmt.Sprintf("#<promise %d>", p.id) }

// registerPromises registers make-promise, promise? and force. It runs only
// when the host granted tasks: a promise waits for the task that is running
// its body, and waiting needs the task runtime.
func (in *Interp) registerPromises() {
	any1 := []Param{{Name: "x", Type: "any"}}

	in.mustRegisterOpaque("make-promise", "A new promise that will run thunk (a procedure of no arguments) on the first force and remember the result.", []Param{{Name: "thunk", Type: "any", Doc: "procedure of no arguments"}}, false, func(c *Call) (Value, error) {
		thunk, err := c.Proc(0)
		if err != nil {
			return Value{}, err
		}
		rt := c.In.tasks
		return ObjectValue(&promiseObj{rt: rt, id: rt.ids.Add(1), once: &onceObj{rt: rt, id: rt.ids.Add(1)}, thunk: thunk}), nil
	})
	in.mustRegisterOpaque("promise?", "True for promises.", any1, false, func(c *Call) (Value, error) {
		_, ok := ObjectOf(c.Args[0]).(*promiseObj)
		return Bool(ok), nil
	})
	in.mustRegister("force", "Run the promise's body if no task has yet and return its value, the same for every caller; a chain of promises is unwrapped. A value that is not a promise is returned as it is.", []Param{{Name: "p", Type: "any"}}, false, func(c *Call) (Value, error) {
		v := c.Args[0]
		for {
			p, ok := ObjectOf(v).(*promiseObj)
			if !ok {
				return v, nil
			}
			var err error
			v, err = c.In.onceDo(c, p.once, p.thunk)
			if err != nil {
				return Value{}, err
			}
		}
	})
}
