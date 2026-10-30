// Package analysis provides static, source-preserving Brote analysis. It never
// evaluates a datum or invokes a host primitive. Positions use LSP UTF-16 units.
package analysis

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/brote/scheme"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

// Symbol describes a builtin or host-provided function. Profiles are data only.
type Symbol struct {
	Name       string   `json:"name"`
	Doc        string   `json:"doc,omitempty"`
	Parameters []string `json:"parameters,omitempty"`
	Variadic   bool     `json:"variadic,omitempty"`
}
type Profile struct {
	Name    string   `json:"name"`
	Symbols []Symbol `json:"symbols"`
}
type Definition struct {
	Name       string
	Range      Range
	Scope      Range
	Function   bool
	Parameters []string
}
type Reference struct {
	Name       string
	Range      Range
	Definition *Definition
}
type checkpoint struct {
	offset   int
	position Position
}

type Document struct {
	checkpoints []checkpoint
	Text        string
	Definitions []*Definition
	References  []Reference
	Diagnostics []Diagnostic
	Symbols     map[string]Symbol
}
type scope struct {
	parent     *scope
	bindings   map[string]*Definition
	start, end int
}
type walker struct{ d *Document }

func Builtins() []Symbol {
	in := scheme.New(scheme.WithOutput(io.Discard), scheme.WithTasks(scheme.TaskLimits{}))
	var out []Symbol
	for _, p := range in.Prims() {
		s := Symbol{Name: p.Name, Doc: p.Meta.Doc, Variadic: p.Meta.Variadic}
		for _, a := range p.Meta.Params {
			name := a.Name
			if a.Optional {
				name = "[" + name + "]"
			}
			s.Parameters = append(s.Parameters, name)
		}
		out = append(out, s)
	}
	return out
}

// Analyze returns lexical definitions/references and reader diagnostics, retaining
// useful results in incomplete documents. It makes no undefined-name claims:
// dynamic bindings and host/module imports cannot always be resolved statically.
func Analyze(text string, profile Profile) *Document {
	d := &Document{Text: text, Symbols: map[string]Symbol{}, Diagnostics: []Diagnostic{}}
	d.indexPositions()
	for _, s := range Builtins() {
		d.Symbols[s.Name] = s
	}
	for _, s := range profile.Symbols {
		d.Symbols[s.Name] = s
	}
	nodes, err := scheme.ReadSyntax(text)
	if err != nil {
		at := len(text)
		var pe *scheme.ParseError
		if errors.As(err, &pe) {
			at = byteAtRunePosition(text, pe.Line-1, pe.Col-1)
		}
		end := at
		if end < len(text) {
			_, n := utf8.DecodeRuneInString(text[end:])
			end += n
		}
		d.Diagnostics = append(d.Diagnostics, Diagnostic{Range: d.rng(at, end), Severity: 1, Message: err.Error(), Source: "brote"})
	}
	w := walker{d}
	root := &scope{bindings: map[string]*Definition{}, end: len(text)}
	w.body(nodes, root)
	w.concurrency(nodes)
	return d
}
func (d *Document) rng(a, b int) Range { return Range{d.Position(a), d.Position(b)} }
func (d *Document) indexPositions() {
	p := Position{}
	d.checkpoints = []checkpoint{{0, p}}
	last := 0
	for offset, r := range d.Text {
		if offset-last >= 64 {
			d.checkpoints = append(d.checkpoints, checkpoint{offset, p})
			last = offset
		}
		if r == '\n' {
			p.Line++
			p.Character = 0
		} else {
			p.Character++
			if r > 0xffff {
				p.Character++
			}
		}
	}
}
func (d *Document) Position(offset int) Position {
	offset = max(0, min(offset, len(d.Text)))
	at := sort.Search(len(d.checkpoints), func(i int) bool { return d.checkpoints[i].offset > offset }) - 1
	start := 0
	p := Position{}
	if at >= 0 {
		start = d.checkpoints[at].offset
		p = d.checkpoints[at].position
	}
	for _, r := range d.Text[start:offset] {
		if r == '\n' {
			p.Line++
			p.Character = 0
		} else {
			p.Character++
			if r > 0xffff {
				p.Character++
			}
		}
	}
	return p
}

func byteAtRunePosition(text string, line, col int) int {
	l, c := 0, 0
	for i, r := range text {
		if l == line && c == col {
			return i
		}
		if r == '\n' {
			l++
			c = 0
		} else {
			c++
		}
	}
	return len(text)
}
func before(a, b Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

// Before reports whether position a sorts before b.
func Before(a, b Position) bool         { return before(a, b) }
func contains(r Range, p Position) bool { return !before(p, r.Start) && before(p, r.End) }
func (d *Document) At(p Position) *Reference {
	for i := range d.References {
		if contains(d.References[i].Range, p) {
			return &d.References[i]
		}
	}
	return nil
}
func (d *Document) DefinitionAt(p Position) *Definition {
	if r := d.At(p); r != nil {
		return r.Definition
	}
	return nil
}
func (d *Document) ReferencesAt(p Position, includeDeclaration bool) []Range {
	def := d.DefinitionAt(p)
	out := []Range{}
	if def == nil {
		return out
	}
	for _, r := range d.References {
		if r.Definition == def && (includeDeclaration || r.Range != def.Range) {
			out = append(out, r.Range)
		}
	}
	return out
}
func (d *Document) Hover(p Position) string {
	r := d.At(p)
	if r == nil {
		return ""
	}
	if r.Definition != nil {
		s := r.Definition
		if s.Function {
			return "(" + s.Name + " " + strings.Join(s.Parameters, " ") + ")"
		}
		return s.Name
	}
	if s, ok := d.Symbols[r.Name]; ok {
		return s.Signature() + "\n\n" + s.Doc
	}
	return ""
}
func (s Symbol) Signature() string {
	args := strings.Join(s.Parameters, " ")
	if s.Variadic {
		args += " ..."
	}
	if args != "" {
		args = " " + args
	}
	return "(" + s.Name + args + ")"
}
func (d *Document) Completions(p Position) []Symbol {
	all := map[string]Symbol{}
	for k, v := range d.Symbols {
		all[k] = v
	}
	// Prefer the narrowest visible scope for names shadowed in nested bindings.
	defs := append([]*Definition(nil), d.Definitions...)
	sort.SliceStable(defs, func(i, j int) bool { return before(defs[i].Scope.Start, defs[j].Scope.Start) })
	for _, def := range defs {
		if contains(def.Scope, p) || p == def.Scope.End {
			all[def.Name] = Symbol{Name: def.Name, Parameters: def.Parameters}
		}
	}
	out := make([]Symbol, 0, len(all))
	for _, s := range all {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func child(s *scope, n *scheme.Syntax) *scope {
	return &scope{parent: s, bindings: map[string]*Definition{}, start: n.Start, end: n.End}
}
func name(n *scheme.Syntax) string {
	if n == nil {
		return ""
	}
	return n.Atom
}
func head(n *scheme.Syntax) string {
	if len(n.Children) == 0 {
		return ""
	}
	return name(n.Children[0])
}
func (w walker) bind(n *scheme.Syntax, s *scope, fun bool) *Definition {
	if n == nil || n.Atom == "" || n.Atom == "." {
		return nil
	}
	if old := s.bindings[n.Atom]; old != nil && old.Range == w.d.rng(n.Start, n.End) {
		return old
	}
	d := &Definition{Name: n.Atom, Range: w.d.rng(n.Start, n.End), Scope: w.d.rng(s.start, s.end), Function: fun}
	s.bindings[d.Name] = d
	w.d.Definitions = append(w.d.Definitions, d)
	return d
}
func (w walker) ref(n *scheme.Syntax, s *scope) {
	if n.Atom == "" || n.Atom == "." {
		return
	}
	var def *Definition
	for at := s; at != nil; at = at.parent {
		if d := at.bindings[n.Atom]; d != nil {
			def = d
			break
		}
	}
	w.d.References = append(w.d.References, Reference{n.Atom, w.d.rng(n.Start, n.End), def})
}
func (w walker) predefine(nodes []*scheme.Syntax, s *scope) {
	for _, n := range nodes {
		if n.Prefix != "" {
			continue
		}
		if head(n) == "begin" {
			w.predefine(n.Children[1:], s)
		}
		if head(n) == "define-syntax" && len(n.Children) >= 2 {
			w.bind(n.Children[1], s, true)
			continue
		}
		if head(n) != "define" || len(n.Children) < 2 {
			continue
		}
		target := n.Children[1]
		if len(target.Children) > 0 {
			d := w.bind(target.Children[0], s, true)
			if d != nil {
				for _, p := range target.Children[1:] {
					if p.Atom != "." {
						d.Parameters = append(d.Parameters, p.Atom)
					}
				}
			}
		} else {
			w.bind(target, s, false)
		}
	}
}
func (w walker) body(nodes []*scheme.Syntax, s *scope) {
	w.predefine(nodes, s)
	for _, n := range nodes {
		w.walk(n, s)
	}
}
func (w walker) params(n *scheme.Syntax, s *scope) {
	if n.Atom != "" {
		w.bind(n, s, false)
		w.ref(n, s)
		return
	}
	for _, p := range n.Children {
		w.bind(p, s, false)
		w.ref(p, s)
	}
}
func (w walker) walk(n *scheme.Syntax, s *scope) {
	if n.Prefix == "quote" || n.Prefix == "vector" {
		return
	}
	if n.Prefix == "quasiquote" {
		for _, c := range n.Children {
			w.quasi(c, s, 1)
		}
		return
	}
	if n.Prefix != "" {
		for _, c := range n.Children {
			w.walk(c, s)
		}
		return
	}
	if n.Atom != "" {
		w.ref(n, s)
		return
	}
	c := n.Children
	if len(c) == 0 {
		return
	}
	switch head(n) {
	case "quote":
		return
	case "quasiquote":
		for _, v := range c[1:] {
			w.quasi(v, s, 1)
		}
		return
	case "define":
		if len(c) < 2 {
			return
		}
		target := c[1]
		if len(target.Children) > 0 {
			local := child(s, n)
			w.ref(target.Children[0], s)
			for _, p := range target.Children[1:] {
				w.bind(p, local, false)
				w.ref(p, local)
			}
			w.body(c[2:], local)
		} else {
			w.ref(target, s)
			w.body(c[2:], s)
		}
		return
	case "define-syntax":
		// The rules are patterns and templates, not code to look names up in.
		if len(c) >= 2 {
			w.ref(c[1], s)
		}
		return
	case "let-syntax", "letrec-syntax":
		if len(c) < 2 {
			return
		}
		local := child(s, n)
		for _, b := range c[1].Children {
			if len(b.Children) > 0 {
				w.bind(b.Children[0], local, true)
				w.ref(b.Children[0], local)
			}
		}
		w.body(c[2:], local)
		return
	case "lambda":
		if len(c) < 2 {
			return
		}
		local := child(s, n)
		w.params(c[1], local)
		w.body(c[2:], local)
		return
	case "let", "let*", "letrec":
		if len(c) < 2 {
			return
		}
		kind := head(n)
		local := child(s, n)
		index := 1
		if c[1].Atom != "" {
			w.bind(c[1], local, true)
			w.ref(c[1], local)
			index++
		}
		if index >= len(c) {
			return
		}
		bindings := c[index].Children
		if kind == "letrec" {
			for _, b := range bindings {
				if len(b.Children) > 0 {
					w.bind(b.Children[0], local, false)
				}
			}
		}
		for _, b := range bindings {
			if len(b.Children) == 0 {
				continue
			}
			initScope := s
			if kind == "letrec" || kind == "let*" {
				initScope = local
			}
			for _, v := range b.Children[1:] {
				w.walk(v, initScope)
			}
			if kind == "let*" {
				local = child(local, n)
			}
			if kind != "letrec" {
				w.bind(b.Children[0], local, false)
			}
			w.ref(b.Children[0], local)
		}
		w.body(c[index+1:], local)
		return
	case "do":
		if len(c) < 2 {
			return
		}
		local := child(s, n)
		for _, b := range c[1].Children {
			if len(b.Children) > 0 {
				w.bind(b.Children[0], local, false)
			}
		}
		for _, b := range c[1].Children {
			if len(b.Children) > 1 {
				w.walk(b.Children[1], s)
			}
			if len(b.Children) > 0 {
				w.ref(b.Children[0], local)
			}
			if len(b.Children) > 2 {
				for _, step := range b.Children[2:] {
					w.walk(step, local)
				}
			}
		}
		for _, v := range c[2:] {
			w.walk(v, local)
		}
		return
	case "guard":
		if len(c) < 2 {
			return
		}
		local := child(s, n)
		if len(c[1].Children) > 0 {
			w.bind(c[1].Children[0], local, false)
			w.ref(c[1].Children[0], local)
			for _, clause := range c[1].Children[1:] {
				w.walk(clause, local)
			}
		}
		w.body(c[2:], s)
		return
	case "begin":
		w.body(c[1:], s)
		return
	}
	// Syntax keywords have no value binding. Their operands still do.
	switch head(n) {
	case "if", "cond", "case", "and", "or", "set!", "when", "unless", "do", "guard":
		c = c[1:]
	}
	for _, v := range c {
		w.walk(v, s)
	}
}
func (w walker) quasi(n *scheme.Syntax, s *scope, depth int) {
	prefix := n.Prefix
	children := n.Children
	if prefix == "" && (head(n) == "quasiquote" || head(n) == "unquote" || head(n) == "unquote-splicing") {
		prefix = head(n)
		children = n.Children[1:]
	}
	if prefix == "quasiquote" {
		depth++
	}
	if prefix == "unquote" || prefix == "unquote-splicing" {
		depth--
		if depth == 0 {
			for _, c := range children {
				w.walk(c, s)
			}
			return
		}
	}
	for _, c := range children {
		w.quasi(c, s, depth)
	}
}

// ValidateProfile rejects ambiguous host metadata instead of silently changing
// which declaration an editor shows.
func ValidateProfile(p Profile) error {
	seen := map[string]bool{}
	for _, s := range p.Symbols {
		if s.Name == "" || seen[s.Name] {
			return fmt.Errorf("empty or duplicate profile symbol %q", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}
