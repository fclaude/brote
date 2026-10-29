package scheme

import (
	"context"
)

// quasi implements quasiquote, including nested ones: an unquote inside a
// quasiquote inside a quasiquote belongs to the inner one, and is kept as
// written (with what it holds processed one level out) until the outer one is
// unquoted. depth is how many quasiquotes enclose x.
func (in *Interp) quasi(t *task, ctx context.Context, x Value, env *Env, depth int) (Value, error) {
	if x.Kind == KVec {
		l, err := in.quasi(t, ctx, List(x.V.Elements()...), env, depth)
		if err != nil {
			return Value{}, err
		}
		items, _ := l.Slice()
		return Vec(items...), nil
	}
	if x.Kind != KPair {
		return x, nil
	}
	if x.P.Car.Kind == KSymbol {
		switch x.P.Car.S {
		case "unquote":
			args, ok := x.P.Cdr.Slice()
			if !ok || len(args) != 1 {
				return Value{}, errorf("unquote", "expected 1 argument")
			}
			if depth == 1 {
				return in.eval(t, ctx, args[0], env)
			}
			return in.quasiWrap("unquote", args[0], t, ctx, env, depth-1)
		case "unquote-splicing":
			args, ok := x.P.Cdr.Slice()
			if !ok || len(args) != 1 {
				return Value{}, errorf("unquote-splicing", "expected 1 argument")
			}
			if depth == 1 {
				return Value{}, errorf("unquote-splicing", "not inside a list")
			}
			return in.quasiWrap("unquote-splicing", args[0], t, ctx, env, depth-1)
		case "quasiquote":
			args, ok := x.P.Cdr.Slice()
			if !ok || len(args) != 1 {
				return Value{}, errorf("quasiquote", "expected 1 argument")
			}
			return in.quasiWrap("quasiquote", args[0], t, ctx, env, depth+1)
		}
	}
	var items []Value
	cur := x
	for first := true; cur.Kind == KPair; first = false {
		// (a . ,b) reads as (a unquote b): an unquote in the tail is the
		// tail's value, not two more elements.
		if !first && cur.P.Car.Kind == KSymbol && cur.P.Car.S == "unquote" {
			break
		}
		el := cur.P.Car
		if depth == 1 && el.Kind == KPair && el.P.Car.Kind == KSymbol && el.P.Car.S == "unquote-splicing" {
			args, ok := el.P.Cdr.Slice()
			if !ok || len(args) != 1 {
				return Value{}, errorf("unquote-splicing", "expected 1 argument")
			}
			v, err := in.eval(t, ctx, args[0], env)
			if err != nil {
				return Value{}, err
			}
			spliced, ok := v.Slice()
			if !ok {
				return Value{}, errorf("unquote-splicing", "expected a list, got %s", Write(v))
			}
			items = append(items, spliced...)
		} else {
			v, err := in.quasi(t, ctx, el, env, depth)
			if err != nil {
				return Value{}, err
			}
			items = append(items, v)
		}
		cur = cur.P.Cdr
	}
	tail := cur
	if cur.Kind != KNil {
		t, err := in.quasi(t, ctx, cur, env, depth)
		if err != nil {
			return Value{}, err
		}
		tail = t
	}
	out := tail
	for i := len(items) - 1; i >= 0; i-- {
		out = Cons(items[i], out)
	}
	return out, nil
}

// quasiWrap is (name x) with x processed at the given depth: the form a nested
// quasiquote keeps for an outer one to deal with.
func (in *Interp) quasiWrap(name string, x Value, t *task, ctx context.Context, env *Env, depth int) (Value, error) {
	v, err := in.quasi(t, ctx, x, env, depth)
	if err != nil {
		return Value{}, err
	}
	return List(Sym(name), v), nil
}
