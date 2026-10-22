package scheme

// hashArg is argument i, checked to be a hash table.
func (c *Call) hashArg(i int) (*Hash, error) {
	if v := c.arg(i); v.Kind == KHash {
		return v.Hash, nil
	}
	return nil, c.typeErr(i, "hash")
}

// keyArg is argument i as a hash key. Only strings, symbols, integers and
// booleans can be keys: equal? on those is a plain comparison, and a float or
// a list would need a rule (is 1.0 the key 1? is a list its contents or its
// address?) that this table would then have to keep.
func (c *Call) keyArg(i int) (hashKey, error) {
	v := c.arg(i)
	k, ok := keyOf(v)
	if !ok {
		return hashKey{}, errorf(c.Name, "argument %d: a hash key is a string, symbol, integer or boolean, got %s %s", i+1, v.Kind, Write(v))
	}
	return k, nil
}

func (in *Interp) registerHashes() {
	h := Param{Name: "h", Type: "hash"}
	key := Param{Name: "key", Type: "any", Doc: "a string, symbol, integer or boolean"}

	in.mustRegister("make-hash", "A new, empty hash table.", nil, false, func(c *Call) (Value, error) {
		return NewHash(), nil
	})
	in.mustRegisterOpaque("hash?", "True for hash tables.", []Param{{Name: "x", Type: "any"}}, false, func(c *Call) (Value, error) {
		return Bool(c.Args[0].Kind == KHash), nil
	})
	in.mustRegister("hash-ref", "The value stored under key. Without a default, a missing key is an error that names it.",
		[]Param{h, key, {Name: "default", Type: "any", Doc: "returned when key is missing", Optional: true}}, false,
		func(c *Call) (Value, error) {
			t, err := c.hashArg(0)
			if err != nil {
				return Value{}, err
			}
			k, err := c.keyArg(1)
			if err != nil {
				return Value{}, err
			}
			if v, ok := t.get(k); ok {
				return v, nil
			}
			if len(c.Args) > 2 {
				return c.Args[2], nil
			}
			return Value{}, errorf(c.Name, "no key %s", Write(c.Args[1]))
		})
	in.mustRegister("hash-set!", "Store a value under key, replacing any value already there.",
		[]Param{h, key, {Name: "v", Type: "any"}}, false, func(c *Call) (Value, error) {
			t, err := c.hashArg(0)
			if err != nil {
				return Value{}, err
			}
			k, err := c.keyArg(1)
			if err != nil {
				return Value{}, err
			}
			t.set(k, c.Args[1], c.Args[2])
			return Void, nil
		})
	in.mustRegister("hash-has?", "True if the table has an entry for key.", []Param{h, key}, false, func(c *Call) (Value, error) {
		t, err := c.hashArg(0)
		if err != nil {
			return Value{}, err
		}
		k, err := c.keyArg(1)
		if err != nil {
			return Value{}, err
		}
		_, ok := t.get(k)
		return Bool(ok), nil
	})
	in.mustRegister("hash-remove!", "Remove the entry for key. A key that is not there is not an error.", []Param{h, key}, false, func(c *Call) (Value, error) {
		t, err := c.hashArg(0)
		if err != nil {
			return Value{}, err
		}
		k, err := c.keyArg(1)
		if err != nil {
			return Value{}, err
		}
		t.remove(k)
		return Void, nil
	})
	in.mustRegisterOpaque("hash-count", "Number of entries in a hash table.", []Param{h}, false, func(c *Call) (Value, error) {
		t, err := c.hashArg(0)
		if err != nil {
			return Value{}, err
		}
		return Int(int64(t.live)), nil
	})
	in.mustRegister("hash-keys", "The keys of a hash table as a list, in the order they were first added.", []Param{h}, false, func(c *Call) (Value, error) {
		t, err := c.hashArg(0)
		if err != nil {
			return Value{}, err
		}
		out := make([]Value, 0, t.live)
		t.each(func(k, _ Value) { out = append(out, k) })
		return List(out...), nil
	})
	in.mustRegister("hash-values", "The values of a hash table as a list, in the order their keys were first added.", []Param{h}, false, func(c *Call) (Value, error) {
		t, err := c.hashArg(0)
		if err != nil {
			return Value{}, err
		}
		out := make([]Value, 0, t.live)
		t.each(func(_, v Value) { out = append(out, v) })
		return List(out...), nil
	})
	in.mustRegister("hash->alist", "The entries of a hash table as an association list, in the order their keys were first added.", []Param{h}, false, func(c *Call) (Value, error) {
		t, err := c.hashArg(0)
		if err != nil {
			return Value{}, err
		}
		out := make([]Value, 0, t.live)
		t.each(func(k, v Value) { out = append(out, Cons(k, v)) })
		return List(out...), nil
	})
	in.mustRegister("alist->hash", "A hash table with the entries of an association list. When a key appears twice the later value wins and the key keeps its first place.",
		[]Param{{Name: "alist", Type: "alist"}}, false, func(c *Call) (Value, error) {
			items, err := c.List(0)
			if err != nil {
				return Value{}, err
			}
			out := NewHash()
			for i, it := range items {
				if it.Kind != KPair {
					return Value{}, errorf(c.Name, "element %d is %s, not a pair", i+1, Write(it))
				}
				k, ok := keyOf(it.P.Car)
				if !ok {
					return Value{}, errorf(c.Name, "element %d: a hash key is a string, symbol, integer or boolean, got %s %s", i+1, it.P.Car.Kind, Write(it.P.Car))
				}
				out.Hash.set(k, it.P.Car, it.P.Cdr)
			}
			return out, nil
		})
	in.mustRegister("hash-update!", "Replace the value under key with (proc value). A missing key uses default as the value, and is an error that names it when there is no default.",
		[]Param{h, key, {Name: "proc", Type: "any", Doc: "procedure of one argument"}, {Name: "default", Type: "any", Doc: "the value to start from when key is missing", Optional: true}}, false,
		func(c *Call) (Value, error) {
			t, err := c.hashArg(0)
			if err != nil {
				return Value{}, err
			}
			k, err := c.keyArg(1)
			if err != nil {
				return Value{}, err
			}
			f, err := c.Proc(2)
			if err != nil {
				return Value{}, err
			}
			cur, ok := t.get(k)
			if !ok {
				if len(c.Args) < 4 {
					return Value{}, errorf(c.Name, "no key %s", Write(c.Args[1]))
				}
				cur = c.Args[3]
			}
			next, err := c.In.Apply(c.Ctx, f, []Value{cur})
			if err != nil {
				return Value{}, err
			}
			t.set(k, c.Args[1], next)
			return Void, nil
		})
}
