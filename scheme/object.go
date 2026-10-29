package scheme

// Object is a native value that Scheme can hold, pass and compare but not
// look into: a task, a channel, a lock. Its identity is the Go value behind
// it, so two Values made from the same Object are eq?.
//
// A KObject Value keeps its Object in the Handle field, which costs the
// Value nothing; Class on that Handle is the object's kind. Credential
// handles (KHandle) are a different kind and are not confused with these.
type Object interface {
	// Kind names what the object is, as a type error should say it:
	// "task", "channel".
	Kind() string
	// String is how the object prints.
	String() string
}

// ObjectValue wraps o as a Scheme value.
func ObjectValue(o Object) Value {
	return Value{Kind: KObject, H: &Handle{Class: o.Kind(), obj: o}}
}

// ObjectOf returns the Object in v, or nil if v is not one.
func ObjectOf(v Value) Object {
	if v.Kind != KObject {
		return nil
	}
	return v.H.obj
}

// objArg is argument i as an object of type T.
func objArg[T Object](c *Call, i int, what string) (T, error) {
	if o, ok := ObjectOf(c.arg(i)).(T); ok {
		return o, nil
	}
	var zero T
	return zero, c.typeErr(i, what)
}
