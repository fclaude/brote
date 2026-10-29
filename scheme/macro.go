package scheme

import (
	"fmt"
	"sync/atomic"
)

// Macros: define-syntax with syntax-rules (docs/scheme.md §3.1).
//
// A macro is a value in the environment, bound under its name like any other,
// so a local variable of the same name hides it and a define-syntax in a body is
// local to that body. Evaluation meets one as the head of a form: the form is
// matched against the macro's patterns, the template of the first that fits is
// filled in, and the result is evaluated in the form's place, in tail position.
// Expansion happens as the form is evaluated, so a macro used in a loop is
// expanded once (the result is kept with the form) and one used before its
// define-syntax has run is an unbound variable, like any other name.
//
// Macros are not fully hygienic. A name a template binds (with let, let*,
// letrec, a named let, lambda or do) is renamed for each use, so it cannot
// capture a name in the use; a name a template only refers to means what it
// means where the macro is used.

// macro is the Object a define-syntax binds.
type macro struct {
	name     string
	literals map[string]bool
	ellipsis string
	rules    []macroRule
}

type macroRule struct {
	pattern  Value // the pattern with its first element (the macro's keyword) dropped
	template Value
	vars     map[string]int  // pattern variable -> how many ellipses it is under
	binders  map[string]bool // names the template binds, which each expansion renames
}

func (m *macro) Kind() string   { return "macro" }
func (m *macro) String() string { return "#<macro " + m.name + ">" }

// macroOf is the macro v holds, or nil.
func macroOf(v Value) *macro {
	if v.Kind != KObject {
		return nil
	}
	m, _ := v.H.obj.(*macro)
	return m
}

// gensyms numbers the names expansion makes up.
var gensyms atomic.Uint64

// parseSyntaxRules reads (syntax-rules [ellipsis] (literal …) (pattern template) …).
func parseSyntaxRules(who, name string, spec Value) (*macro, error) {
	items, ok := spec.Slice()
	if !ok || len(items) < 2 || items[0].Kind != KSymbol || items[0].S != "syntax-rules" {
		return nil, errorf(who, "%s: expected (syntax-rules (literal …) (pattern template) …)", name)
	}
	m := &macro{name: name, literals: map[string]bool{}, ellipsis: "..."}
	rest := items[1:]
	if rest[0].Kind == KSymbol { // a custom ellipsis, as in (syntax-rules ::: (lits) …)
		m.ellipsis = rest[0].S
		rest = rest[1:]
		if len(rest) == 0 {
			return nil, errorf(who, "%s: expected a list of literals", name)
		}
	}
	lits, ok := rest[0].Slice()
	if !ok {
		return nil, errorf(who, "%s: the literals must be a list of names", name)
	}
	for _, l := range lits {
		if l.Kind != KSymbol {
			return nil, errorf(who, "%s: literal %s is not a name", name, Write(l))
		}
		m.literals[l.S] = true
	}
	for _, r := range rest[1:] {
		parts, ok := r.Slice()
		if !ok || len(parts) != 2 {
			return nil, errorf(who, "%s: a rule is (pattern template), got %s", name, Write(r))
		}
		pat := parts[0]
		if pat.Kind != KPair {
			return nil, errorf(who, "%s: a pattern is a list that starts with the keyword, got %s", name, Write(pat))
		}
		rule := macroRule{pattern: pat.P.Cdr, template: parts[1], vars: map[string]int{}}
		if err := m.patternVars(rule.pattern, 0, rule.vars); err != nil {
			return nil, errorf(who, "%s: %v", name, err)
		}
		rule.binders = map[string]bool{}
		m.findBinders(rule.template, rule.vars, rule.binders)
		m.rules = append(m.rules, rule)
	}
	return m, nil
}

func (m *macro) isEllipsis(v Value) bool { return v.Kind == KSymbol && v.S == m.ellipsis }

// patternVars records the variables of pattern p and how deep each is.
func (m *macro) patternVars(p Value, depth int, vars map[string]int) error {
	switch p.Kind {
	case KSymbol:
		if m.literals[p.S] || p.S == "_" || m.isEllipsis(p) {
			return nil
		}
		if _, dup := vars[p.S]; dup {
			return fmt.Errorf("%s appears twice in a pattern", p.S)
		}
		vars[p.S] = depth
	case KPair:
		for p.Kind == KPair {
			if m.isEllipsis(p.P.Cdr.carOr()) {
				if err := m.patternVars(p.P.Car, depth+1, vars); err != nil {
					return err
				}
				p = p.P.Cdr.P.Cdr
				continue
			}
			if err := m.patternVars(p.P.Car, depth, vars); err != nil {
				return err
			}
			p = p.P.Cdr
		}
		return m.patternVars(p, depth, vars)
	case KVec:
		return m.patternVars(List(p.V.Elements()...), depth, vars)
	}
	return nil
}

// carOr is the car of a pair, or void for anything else.
func (v Value) carOr() Value {
	if v.Kind == KPair {
		return v.P.Car
	}
	return Void
}

// findBinders collects the names a template binds itself: those that are not
// pattern variables, in the binding positions of let, let*, letrec, named let,
// lambda and do.
func (m *macro) findBinders(t Value, vars map[string]int, out map[string]bool) {
	if t.Kind != KPair {
		return
	}
	add := func(v Value) {
		if v.Kind == KSymbol && !m.isEllipsis(v) && v.S != "." {
			if _, isVar := vars[v.S]; !isVar {
				out[v.S] = true
			}
		}
	}
	items, proper := t.Slice()
	if proper && len(items) >= 2 && items[0].Kind == KSymbol {
		switch items[0].S {
		case "quote":
			return
		case "let", "let*", "letrec", "do":
			binds := items[1]
			if items[0].S == "let" && binds.Kind == KSymbol && len(items) >= 3 {
				add(binds) // a named let
				binds = items[2]
			}
			for b := binds; b.Kind == KPair; b = b.P.Cdr {
				if b.P.Car.Kind == KPair {
					add(b.P.Car.P.Car)
				}
			}
		case "lambda":
			params := items[1]
			for ; params.Kind == KPair; params = params.P.Cdr {
				add(params.P.Car)
			}
			add(params)
		}
	}
	for ; t.Kind == KPair; t = t.P.Cdr {
		m.findBinders(t.P.Car, vars, out)
	}
}

// binding is what a pattern variable matched: a form, or at a variable under
// ellipsis, one binding for each time the ellipsis matched.
type binding struct {
	form Value
	seq  []*binding
	deep bool // seq is meaningful (the variable is under an ellipsis)
}

type bindings map[string]*binding

// expand rewrites form, a use of the macro, by the first rule it matches.
func (m *macro) expand(form Value) (Value, error) {
	for _, r := range m.rules {
		b := bindings{}
		if m.match(r.pattern, form.P.Cdr, b) {
			rename := map[string]string{}
			if len(r.binders) > 0 {
				n := gensyms.Add(1)
				for name := range r.binders {
					rename[name] = fmt.Sprintf("%s~%d", name, n)
				}
			}
			out, err := (&expander{macro: m}).instantiate(r.template, b, rename, false)
			if err != nil {
				return Value{}, errorf(m.name, "%v", err)
			}
			if out.Kind == KPair && form.P.loc != nil {
				// Evaluation of the result reports the use, not the template.
				cp := &Pair{Car: out.P.Car, Cdr: out.P.Cdr, loc: form.P.loc}
				out = Value{Kind: KPair, P: cp}
			}
			return out, nil
		}
	}
	return Value{}, errorf(m.name, "no pattern matches %s", Write(form))
}

func (m *macro) match(p, f Value, b bindings) bool {
	switch p.Kind {
	case KSymbol:
		switch {
		case m.literals[p.S]:
			return f.Kind == KSymbol && f.S == p.S
		case p.S == "_":
			return true
		}
		b[p.S] = &binding{form: f}
		return true
	case KNil:
		return f.Kind == KNil
	case KPair:
		return m.matchList(p, f, b)
	case KVec:
		if f.Kind != KVec {
			return false
		}
		return m.match(List(p.V.Elements()...), List(f.V.Elements()...), b)
	}
	return Equal(p, f)
}

func (m *macro) matchList(p, f Value, b bindings) bool {
	for p.Kind == KPair {
		if m.isEllipsis(p.P.Cdr.carOr()) {
			sub := p.P.Car
			after := p.P.Cdr.P.Cdr
			// How many forms the patterns after the ellipsis need.
			need := 0
			for a := after; a.Kind == KPair; a = a.P.Cdr {
				need++
			}
			var items []Value
			for g := f; g.Kind == KPair; g = g.P.Cdr {
				items = append(items, g.P.Car)
			}
			n := len(items) - need
			if n < 0 {
				return false
			}
			vars := map[string]int{}
			if m.patternVars(sub, 0, vars) != nil {
				return false
			}
			seqs := make(map[string][]*binding, len(vars))
			for name := range vars {
				seqs[name] = make([]*binding, 0, n)
			}
			for i := 0; i < n; i++ {
				nb := bindings{}
				if !m.match(sub, items[i], nb) {
					return false
				}
				for name := range vars {
					seqs[name] = append(seqs[name], nb[name])
				}
			}
			for name := range vars {
				b[name] = &binding{seq: seqs[name], deep: true}
			}
			for i := 0; i < n; i++ {
				f = f.P.Cdr
			}
			p = after
			continue
		}
		if f.Kind != KPair || !m.match(p.P.Car, f.P.Car, b) {
			return false
		}
		p, f = p.P.Cdr, f.P.Cdr
	}
	return m.match(p, f, b) // the tail: () must meet (), a name takes the rest
}

// maxExpansion bounds the pairs one expansion builds. A macro that expands to a
// use of itself with more in it each time would otherwise double its way to all
// of memory.
const maxExpansion = 1 << 20

// expander is one expansion at work.
type expander struct {
	*macro
	built int // pairs made so far
}

// instantiate fills in template t. rename maps a name the template binds to
// the name this expansion gives it; quoted is true inside a quote, where names
// are data and keep their spelling. escaped is true after (... ...), where the
// ellipsis is an ordinary name.
func (m *expander) instantiate(t Value, b bindings, rename map[string]string, quoted bool) (Value, error) {
	return m.inst(t, b, rename, quoted, false)
}

func (m *expander) inst(t Value, b bindings, rename map[string]string, quoted, escaped bool) (Value, error) {
	switch t.Kind {
	case KSymbol:
		if bd, ok := b[t.S]; ok {
			if bd.deep {
				return Value{}, fmt.Errorf("%s is followed by an ellipsis in the pattern and must be in the template", t.S)
			}
			return bd.form, nil
		}
		if n, ok := rename[t.S]; ok && !quoted {
			return Sym(n), nil
		}
		return t, nil
	case KVec:
		l, err := m.inst(List(t.V.Elements()...), b, rename, quoted, escaped)
		if err != nil {
			return Value{}, err
		}
		items, _ := l.Slice()
		return Vec(items...), nil
	case KPair:
	default:
		return t, nil
	}
	// (... template): the template with its ellipses taken literally.
	if !escaped && m.isEllipsis(t.P.Car) && t.P.Cdr.Kind == KPair {
		return m.inst(t.P.Cdr.P.Car, b, rename, quoted, true)
	}
	if t.P.Car.Kind == KSymbol && t.P.Car.S == "quote" {
		quoted = true
	}
	var items []Value
	cur := t
	for cur.Kind == KPair {
		if m.built++; m.built > maxExpansion {
			return Value{}, fmt.Errorf("the expansion is larger than %d forms", maxExpansion)
		}
		el := cur.P.Car
		cur = cur.P.Cdr
		depth := 0
		if !escaped {
			for cur.Kind == KPair && m.isEllipsis(cur.P.Car) {
				depth++
				cur = cur.P.Cdr
			}
		}
		if depth == 0 {
			v, err := m.inst(el, b, rename, quoted, escaped)
			if err != nil {
				return Value{}, err
			}
			items = append(items, v)
			continue
		}
		vs, err := m.instEllipsis(el, b, rename, quoted, depth)
		if err != nil {
			return Value{}, err
		}
		items = append(items, vs...)
	}
	tail := Nil
	if cur.Kind != KNil {
		v, err := m.inst(cur, b, rename, quoted, escaped)
		if err != nil {
			return Value{}, err
		}
		tail = v
	}
	out := tail
	for i := len(items) - 1; i >= 0; i-- {
		out = Cons(items[i], out)
	}
	if out.Kind == KPair && t.P.loc != nil {
		out.P.loc = t.P.loc
	}
	return out, nil
}

// instEllipsis is el followed by depth ellipses: el filled in once for each
// time the variables in it matched, the results flattened depth levels.
func (m *expander) instEllipsis(el Value, b bindings, rename map[string]string, quoted bool, depth int) ([]Value, error) {
	used := map[string]bool{}
	m.templateVars(el, b, used)
	var iterate []string
	n := -1
	for name := range used {
		if bd := b[name]; bd.deep {
			iterate = append(iterate, name)
			if n >= 0 && len(bd.seq) != n {
				return nil, fmt.Errorf("the variables under one ellipsis matched different numbers of forms (%d and %d)", n, len(bd.seq))
			}
			n = len(bd.seq)
		}
	}
	if len(iterate) == 0 {
		return nil, fmt.Errorf("an ellipsis in the template follows nothing that was followed by one in the pattern")
	}
	var out []Value
	for i := 0; i < n; i++ {
		if m.built++; m.built > maxExpansion {
			return nil, fmt.Errorf("the expansion is larger than %d forms", maxExpansion)
		}
		nb := make(bindings, len(b))
		for k, v := range b {
			nb[k] = v
		}
		for _, name := range iterate {
			nb[name] = b[name].seq[i]
		}
		if depth > 1 {
			vs, err := m.instEllipsis(el, nb, rename, quoted, depth-1)
			if err != nil {
				return nil, err
			}
			out = append(out, vs...)
			continue
		}
		v, err := m.inst(el, nb, rename, quoted, false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// templateVars records the pattern variables t uses.
func (m *macro) templateVars(t Value, b bindings, out map[string]bool) {
	switch t.Kind {
	case KSymbol:
		if _, ok := b[t.S]; ok {
			out[t.S] = true
		}
	case KPair:
		for ; t.Kind == KPair; t = t.P.Cdr {
			m.templateVars(t.P.Car, b, out)
		}
		m.templateVars(t, b, out)
	case KVec:
		for _, e := range t.V.Elements() {
			m.templateVars(e, b, out)
		}
	}
}

// evalDefineSyntax is (define-syntax name (syntax-rules …)).
func (in *Interp) evalDefineSyntax(form []Value, env *Env) (Value, error) {
	if len(form) != 2 || form[0].Kind != KSymbol {
		return Value{}, errorf("define-syntax", "expected (define-syntax name (syntax-rules …))")
	}
	name := form[0].S
	if in.isSF[name] {
		return Value{}, errorf("define-syntax", "%s is a special form and cannot be redefined", name)
	}
	m, err := parseSyntaxRules("define-syntax", name, form[1])
	if err != nil {
		return Value{}, err
	}
	env.Define(name, ObjectValue(m))
	return Void, nil
}

// evalLetSyntax is (let-syntax ((name (syntax-rules …)) …) body …), also
// letrec-syntax: the macros are known in the body only.
func (in *Interp) evalLetSyntax(who string, form []Value, env *Env) (Value, *Env, error) {
	if len(form) < 2 {
		return Value{}, nil, errorf(who, "expected ((name (syntax-rules …)) …) and a body")
	}
	specs, ok := form[0].Slice()
	if !ok {
		return Value{}, nil, errorf(who, "the bindings must be a list")
	}
	nenv := NewEnv(env)
	for _, s := range specs {
		parts, ok := s.Slice()
		if !ok || len(parts) != 2 || parts[0].Kind != KSymbol {
			return Value{}, nil, errorf(who, "a binding is (name (syntax-rules …)), got %s", Write(s))
		}
		if in.isSF[parts[0].S] {
			return Value{}, nil, errorf(who, "%s is a special form and cannot be redefined", parts[0].S)
		}
		m, err := parseSyntaxRules(who, parts[0].S, parts[1])
		if err != nil {
			return Value{}, nil, err
		}
		nenv.Define(parts[0].S, ObjectValue(m))
	}
	body := form[1:]
	return Cons(Sym("begin"), List(body...)), nenv, nil
}

// expandUse expands the form x, a use of macro m, keeping the result with the
// form so that evaluating it again does not expand it again.
func (in *Interp) expandUse(m *macro, x Value) (Value, error) {
	if c := x.P.expansion.Load(); c != nil && c.m == m {
		return c.out, nil
	}
	out, err := m.expand(x)
	if err != nil {
		return Value{}, err
	}
	x.P.expansion.Store(&expansion{m: m, out: out})
	return out, nil
}
