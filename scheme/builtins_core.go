package scheme

// mustRegister registers a builtin; a bad declaration is a programming
// error, so it panics at startup rather than failing later.
func (in *Interp) mustRegister(name, doc string, params []Param, variadic bool, h func(c *Call) (Value, error)) {
	in.mustRegisterBuiltin(name, doc, params, variadic, false, h)
}

// mustRegisterOpaque declares a measurement or predicate whose result does
// not carry its arguments' provenance (SPEC L2). Ordinary builtins propagate.
func (in *Interp) mustRegisterOpaque(name, doc string, params []Param, variadic bool, h func(c *Call) (Value, error)) {
	in.mustRegisterBuiltin(name, doc, params, variadic, true, h)
}

func (in *Interp) mustRegisterBuiltin(name, doc string, params []Param, variadic, opaque bool, h func(c *Call) (Value, error)) {
	err := in.Register(&Prim{
		Name:    name,
		Meta:    Meta{Doc: doc, Params: params, Variadic: variadic, Origin: "builtin", TaintOpaque: opaque},
		Handler: h,
	})
	if err != nil {
		panic(err)
	}
}

func (in *Interp) registerBuiltins() {
	in.registerNumbers()
	in.registerLists()
	in.registerStrings()
	in.registerVectors()
	in.registerHashes()
	in.registerCore()
	in.registerJSON()
	in.registerMeta()
	in.registerDoc()
	in.registerChars()
	in.registerPorts()
	in.registerMutation()
	in.registerCharStrings()
	in.registerTaint()
	if in.tasks != nil {
		in.registerTasks()
	}
}

func (in *Interp) registerCore() {
	any1 := []Param{{Name: "x", Type: "any"}}
	any2 := []Param{{Name: "a", Type: "any"}, {Name: "b", Type: "any"}}

	in.mustRegisterOpaque("not", "True only for #f.", any1, false, func(c *Call) (Value, error) {
		return Bool(!c.Args[0].IsTrue()), nil
	})
	in.mustRegisterOpaque("boolean?", "True for #t and #f.", any1, false, func(c *Call) (Value, error) {
		return Bool(c.Args[0].Kind == KBool), nil
	})
	in.mustRegisterOpaque("eq?", "Identity for pairs and procedures; value for everything else.", any2, false, func(c *Call) (Value, error) {
		return Bool(Eqv(c.Args[0], c.Args[1])), nil
	})
	in.mustRegisterOpaque("eqv?", "Alias of eq?.", any2, false, func(c *Call) (Value, error) {
		return Bool(Eqv(c.Args[0], c.Args[1])), nil
	})
	in.mustRegisterOpaque("equal?", "Structural equality.", any2, false, func(c *Call) (Value, error) {
		return Bool(Equal(c.Args[0], c.Args[1])), nil
	})
	in.mustRegisterOpaque("procedure?", "True for closures and primitives.", any1, false, func(c *Call) (Value, error) {
		return Bool(c.Args[0].IsProcedure()), nil
	})
	in.mustRegisterOpaque("handle?", "True for credential handles.", any1, false, func(c *Call) (Value, error) {
		return Bool(c.Args[0].Kind == KHandle), nil
	})
	in.mustRegister("void", "The void value.", nil, false, func(c *Call) (Value, error) {
		return Void, nil
	})
	in.mustRegister("error", "Raise an error with a message and irritants.", []Param{{Name: "message", Type: "string"}}, true, func(c *Call) (Value, error) {
		msg, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		return Value{}, &Error{Message: msg, Irritants: c.Args[1:]}
	})

	optPort := Param{Name: "port", Type: "any", Optional: true}
	in.mustRegister("display", "Print x in human form, to a port or the current output.", []Param{{Name: "x", Type: "any"}, optPort}, false, func(c *Call) (Value, error) {
		p, err := c.outPort(1)
		if err != nil {
			return Value{}, err
		}
		return Void, p.write(c, Display(c.Args[0]), DeepTaint(c.Args[0]))
	})
	in.mustRegister("write", "Print x in machine form, to a port or the current output.", []Param{{Name: "x", Type: "any"}, optPort}, false, func(c *Call) (Value, error) {
		p, err := c.outPort(1)
		if err != nil {
			return Value{}, err
		}
		return Void, p.write(c, Write(c.Args[0]), DeepTaint(c.Args[0]))
	})
	in.mustRegister("newline", "Print a newline, to a port or the current output.", []Param{optPort}, false, func(c *Call) (Value, error) {
		p, err := c.outPort(0)
		if err != nil {
			return Value{}, err
		}
		return Void, p.write(c, "\n", Taint{})
	})
}
