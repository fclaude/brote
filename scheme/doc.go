package scheme

import (
	"sort"
	"strings"
)

// specialFormDocs says what each special form looks like and does, for Doc.
// docs/scheme.md §3 is the full description.
var specialFormDocs = map[string]string{
	"define":                "(define name expr)\n(define (name param … [. rest]) body …)\nBind a name in the current environment; the second form makes a procedure.",
	"lambda":                "(lambda (param … [. rest]) body …)\n(lambda args body …)\nMake a procedure. A string before the body describes it (see doc).",
	"if":                    "(if test then [else])\nEvaluate then when test is true, else otherwise (void if it is missing).",
	"let":                   "(let ((name expr) …) body …)\n(let loop ((name expr) …) body …)\nBind names in parallel; the named form makes loop a procedure the body can call.",
	"let*":                  "(let* ((name expr) …) body …)\nBind names one after the other, each seeing the ones before.",
	"letrec":                "(letrec ((name expr) …) body …)\nBind names that may refer to each other.",
	"begin":                 "(begin expr …)\nEvaluate in order; the value is the last one's.",
	"set!":                  "(set! name expr)\nChange an existing binding.",
	"quote":                 "(quote datum)  or  'datum\nThe datum itself, unevaluated.",
	"quasiquote":            "(quasiquote template)  or  `template\nLike quote, but (unquote e)  or  ,e is replaced by e's value and ,@e by the elements of the list e.",
	"unquote":               "(unquote e)  or  ,e\nInside quasiquote: the value of e.",
	"unquote-splicing":      "(unquote-splicing e)  or  ,@e\nInside quasiquote: the elements of the list e.",
	"cond":                  "(cond (test body …) … [(else body …)])\nThe body of the first clause whose test is true; (test => proc) passes the test's value to proc.",
	"case":                  "(case key ((datum …) body …) … [(else body …)])\nThe body of the first clause with a datum equal? to key.",
	"else":                  "else\nThe last clause of cond and case.",
	"=>":                    "=>\nIn a cond clause, passes the test's value to a procedure.",
	"and":                   "(and expr …)\nThe first false value, or the last value; #t for no expressions.",
	"or":                    "(or expr …)\nThe first true value, or #f.",
	"do":                    "(do ((name init [step]) …) (test [expr …]) body …)\nLoop until test is true, then evaluate the exprs.",
	"when":                  "(when test expr …)\nEvaluate the exprs if test is true.",
	"unless":                "(unless test expr …)\nEvaluate the exprs if test is false.",
	"guard":                 "(guard (var clause …) body …)\nEvaluate body; if it raises an error, bind var to the condition and try the clauses like cond. See error.",
	"define-syntax":         "(define-syntax name (syntax-rules (literal …) (pattern template) …))\nDefine a macro: a use of name that matches a pattern is replaced by the template, with the pattern's variables filled in; x … repeats what comes before it.",
	"let-syntax":            "(let-syntax ((name (syntax-rules …)) …) body …)\nDefine macros that are known in the body only.",
	"letrec-syntax":         "(letrec-syntax ((name (syntax-rules …)) …) body …)\nLike let-syntax; the macros may use each other.",
	"syntax-rules":          "(syntax-rules (literal …) (pattern template) …)\nThe rules of a macro; only valid inside define-syntax and let-syntax.",
	"with-output-to-string": "(with-output-to-string expr …)\nEvaluate the exprs and return what they displayed or wrote, as a string.",
}

// SpecialForms is the names the language reserves for its special forms.
func SpecialForms() []string { return append([]string(nil), specialForms...) }

// Names is every name a program can use without defining it, or has defined:
// the global environment, the primitives and the special forms, sorted.
func (in *Interp) Names() []string {
	seen := map[string]bool{}
	for n := range in.global.Snapshot() {
		seen[n] = true
	}
	for _, p := range in.Prims() {
		seen[p.Name] = true
	}
	for _, n := range specialForms {
		seen[n] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Doc describes what name is in the global environment: the way it is
// called on the first line or lines, and what it does under that. It reports
// false if the name is not a procedure or special form.
func (in *Interp) Doc(name string) (string, bool) {
	if v, ok := in.global.Lookup(name); ok {
		return DocOf(name, v)
	}
	if d, ok := specialFormDocs[name]; ok {
		return d, true
	}
	return "", false
}

// DocOf describes a procedure that is called name. A primitive has its
// declaration; a procedure written in Scheme has its parameters and the string
// ahead of its body, if there is one.
func DocOf(name string, v Value) (string, bool) {
	switch v.Kind {
	case KPrim:
		sig := []string{name}
		for _, p := range v.Prim.Meta.Params {
			if p.Optional {
				sig = append(sig, "["+p.Name+"]")
			} else {
				sig = append(sig, p.Name)
			}
		}
		if v.Prim.Meta.Variadic {
			sig = append(sig, "…")
		}
		return "(" + strings.Join(sig, " ") + ")\n" + v.Prim.Meta.Doc, true
	case KObject:
		if m := macroOf(v); m != nil {
			lines := make([]string, len(m.rules))
			for i, r := range m.rules {
				lines[i] = Write(Cons(Sym(name), r.pattern))
			}
			return strings.Join(lines, "\n") + "\nA macro.", true
		}
	case KProc:
		c := v.Proc
		sig := append([]string{name}, c.Params...)
		if c.Rest != "" {
			sig = append(sig, ".", c.Rest)
		}
		out := "(" + strings.Join(sig, " ") + ")"
		if c.Doc != "" {
			out += "\n" + c.Doc
		}
		return out, true
	}
	return "", false
}

func (in *Interp) registerDoc() {
	in.mustRegister("doc", "How a procedure or special form is called, and what it does, as text; #f if there is nothing to say. The argument is a symbol or a procedure.", []Param{{Name: "name", Type: "any"}}, false, func(c *Call) (Value, error) {
		switch a := c.Args[0]; a.Kind {
		case KSymbol:
			if d, ok := c.In.Doc(a.S); ok {
				return Str(d), nil
			}
		case KProc:
			if d, ok := DocOf(a.Proc.DisplayName(), a); ok {
				return Str(d), nil
			}
		case KPrim:
			if d, ok := DocOf(a.Prim.Name, a); ok {
				return Str(d), nil
			}
		default:
			return Value{}, c.typeErr(0, "symbol or procedure")
		}
		return False, nil
	})
	in.mustRegister("apropos", "The names of the procedures and special forms whose name contains the text, sorted, as a list of symbols.", []Param{{Name: "text", Type: "string"}}, false, func(c *Call) (Value, error) {
		text, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		var out []Value
		for _, n := range c.In.Names() {
			if !strings.Contains(n, text) {
				continue
			}
			if _, ok := c.In.Doc(n); ok {
				out = append(out, Sym(n))
			}
		}
		return List(out...), nil
	})
}
