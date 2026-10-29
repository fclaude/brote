package scheme

// Changing a pair (set-car!, set-cdr!).
//
// Pairs are plain values that every task reads without a lock, as they are
// everywhere in the interpreter, so a pair cannot be changed while anything else
// might be reading it. Changing one is therefore allowed only to a program that
// is running alone: one evaluation, no tasks alive besides it. The check and the
// change happen under quiesce, which also guards starting an evaluation or a
// task, so none can begin in between. A program that wants shared, changing
// data uses vectors and hash tables, which lock, or atoms.

// aloneErr says why a pair cannot be changed now, or returns nil. The caller
// holds in.quiesce.
func (in *Interp) aloneErr(c *Call) error {
	if in.running > 1 {
		return errorf(c.Name, "a pair can be changed only while nothing else is running, and another evaluation is; share changing data in a vector or a hash table")
	}
	if rt := in.tasks; rt != nil {
		rt.mu.Lock()
		alive := rt.alive
		rt.mu.Unlock()
		if alive > 1 {
			return errorf(c.Name, "a pair can be changed only while nothing else is running, and %d task(s) are alive; wait for them first, or share changing data in a vector or a hash table", alive-1)
		}
	}
	return nil
}

// reaches reports whether target is v or can be reached from it through the
// cars and cdrs of pairs.
func reaches(target *Pair, v Value) bool {
	seen := map[*Pair]bool{}
	var stack []*Pair
	push := func(v Value) {
		if v.Kind == KPair && !seen[v.P] {
			seen[v.P] = true
			stack = append(stack, v.P)
		}
	}
	push(v)
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p == target {
			return true
		}
		push(p.Car)
		push(p.Cdr)
	}
	return false
}

// setPair replaces the car or the cdr of p with x.
func (in *Interp) setPair(c *Call, p *Pair, car bool, x Value) error {
	in.quiesce.Lock()
	defer in.quiesce.Unlock()
	if err := in.aloneErr(c); err != nil {
		return err
	}
	// Lists are finite: every walk over one in Go, such as length or append,
	// ends only at the end of the list.
	if reaches(p, x) {
		return errorf(c.Name, "that would make a circular structure out of pairs; a list must end")
	}
	if car {
		p.Car = x
	} else {
		p.Cdr = x
	}
	// What the evaluator kept about p as code is out of date.
	p.args.Store(nil)
	p.expansion.Store(nil)
	return nil
}

func (in *Interp) registerMutation() {
	params := []Param{{Name: "pair", Type: "any"}, {Name: "x", Type: "any"}}
	in.mustRegister("set-car!", "Replace the first element of a pair. Only for a program running alone (no other evaluation, no tasks alive); a circular result is an error.", params, false, func(c *Call) (Value, error) {
		p, err := c.Pair(0)
		if err != nil {
			return Value{}, err
		}
		return Void, c.In.setPair(c, p, true, c.Args[1])
	})
	in.mustRegister("set-cdr!", "Replace the rest of a pair. Only for a program running alone (no other evaluation, no tasks alive); a circular result is an error.", params, false, func(c *Call) (Value, error) {
		p, err := c.Pair(0)
		if err != nil {
			return Value{}, err
		}
		return Void, c.In.setPair(c, p, false, c.Args[1])
	})
}
