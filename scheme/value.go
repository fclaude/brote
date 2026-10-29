// Package scheme is Brote's Scheme: a small language for a Go program to
// embed, and the only surface through which Scheme code reaches the host is
// the primitives the host registers.
//
// New makes an interpreter; Register adds host operations; EvalString, Apply
// and LoadFile run programs under a context that can cancel them; SetGate puts
// a policy in front of every call from Scheme into Go. The language is
// specified in docs/scheme.md and embedding is described in docs/embedding.md.
// This package implements the language document and nothing more. eval and
// apply are fixed Go functions; loading Scheme code can grow the primitive
// table and the global environment but cannot change evaluation (ADR 7 in
// Nodwyn, where this package was written).
package scheme

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Kind tags a Value.
type Kind uint8

const (
	KNil    Kind = iota // the empty list ()
	KBool               // #t / #f
	KInt                // int64
	KFloat              // float64
	KString             // immutable UTF-8
	KSymbol             // interned by name
	KPair               // cons cell
	KProc               // closure
	KPrim               // primitive (Go handler)
	KHandle             // opaque credential handle (P3)
	KVec                // vector: a mutable, indexable array
	KVoid               // the unspecified value
	KHash               // hash table: a mutable map from strings, symbols, integers and booleans
)

func (k Kind) String() string {
	switch k {
	case KNil:
		return "()"
	case KBool:
		return "boolean"
	case KInt:
		return "integer"
	case KFloat:
		return "float"
	case KString:
		return "string"
	case KSymbol:
		return "symbol"
	case KPair:
		return "pair"
	case KProc, KPrim:
		return "procedure"
	case KHandle:
		return "handle"
	case KVec:
		return "vector"
	case KVoid:
		return "void"
	case KHash:
		return "hash"
	}
	return "unknown"
}

// Value is a Scheme value: one tagged struct, copied by value (ADR 1).
// Exactly one payload field is meaningful for a given Kind.
type Value struct {
	Kind  Kind
	Taint Taint

	I    int64
	F    float64
	S    string // KString and KSymbol
	P    *Pair
	Proc *Closure
	Prim *Prim
	H    *Handle
	V    *Vector // KVec
	Hash *Hash   // KHash
}

// Vector is the shared storage behind a vector value; vector-set! mutates
// it in place and identity (eq?) is pointer identity. Tasks share vectors,
// so every access goes through a method that takes the vector's lock: one
// operation is atomic, a sequence of them is not.
type Vector struct {
	mu    sync.RWMutex
	items []Value
}

// Len is the number of elements.
func (v *Vector) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.items)
}

// At is element i, and false if i is out of range.
func (v *Vector) At(i int) (Value, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if i < 0 || i >= len(v.items) {
		return Value{}, false
	}
	return v.items[i], true
}

// SetAt stores x as element i, and reports false if i is out of range.
func (v *Vector) SetAt(i int, x Value) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if i < 0 || i >= len(v.items) {
		return false
	}
	v.items[i] = x
	return true
}

// Elements is a copy of the elements, taken at one instant.
func (v *Vector) Elements() []Value {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return append([]Value(nil), v.items...)
}

// Pair is a cons cell.
type Pair struct {
	Car, Cdr Value
}

// Closure is a lambda plus its defining environment.
type Closure struct {
	Name   string   // given when the closure is made; "" if anonymous
	Params []string // required positional parameters
	Rest   string   // name bound to the remaining arguments, or ""
	Body   []Value  // at least one expression
	Env    *Env

	// named is the name define gave an anonymous closure afterwards. The
	// closure may be running in another task by then, so Name is never
	// written after construction.
	named atomic.Pointer[string]
}

// DisplayName is the name the closure prints and reports errors under:
// Name, or else the first name define bound it to; "" if it has neither.
func (c *Closure) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	if n := c.named.Load(); n != nil {
		return *n
	}
	return ""
}

// nameIfAnonymous gives an anonymous closure the name define bound it to.
// The first name wins.
func (c *Closure) nameIfAnonymous(name string) {
	if c.Name == "" {
		c.named.CompareAndSwap(nil, &name)
	}
}

// Handle is an opaque reference to a credential in the keychain (P3). It
// carries a class and an id and never the value; no builtin can turn it into
// a string.
type Handle struct {
	Class string
	ID    string
}

// HandleValue wraps a handle as a value tainted with its credential class
// (L2, Phase 6), so anything derived from it keeps the provenance.
func HandleValue(h *Handle) Value {
	return Value{Kind: KHandle, H: h, Taint: TaintOf(CredTag(h.Class))}
}

// Constructors and constants.
var (
	Nil   = Value{Kind: KNil}
	True  = Value{Kind: KBool, I: 1}
	False = Value{Kind: KBool, I: 0}
	Void  = Value{Kind: KVoid}
)

// Bool, Int, Float, Str, Sym and Cons build a value of each kind; the
// result carries no provenance.
func Bool(b bool) Value {
	if b {
		return True
	}
	return False
}

// Int builds an integer.
func Int(n int64) Value { return Value{Kind: KInt, I: n} }

// Float builds a float.
func Float(f float64) Value { return Value{Kind: KFloat, F: f} }

// Str builds a string.
func Str(s string) Value { return Value{Kind: KString, S: s} }

// Sym builds a symbol.
func Sym(s string) Value { return Value{Kind: KSymbol, S: s} }

// Cons builds a pair.
func Cons(a, d Value) Value { return Value{Kind: KPair, P: &Pair{Car: a, Cdr: d}} }

// Vec builds a vector from values.
func Vec(vs ...Value) Value { return Value{Kind: KVec, V: &Vector{items: append([]Value(nil), vs...)}} }

// List builds a proper list.
func List(vs ...Value) Value {
	out := Nil
	for i := len(vs) - 1; i >= 0; i-- {
		out = Cons(vs[i], out)
	}
	return out
}

// IsTrue implements Scheme truthiness: only #f is false.
func (v Value) IsTrue() bool { return !(v.Kind == KBool && v.I == 0) }

// IsNumber reports KInt or KFloat.
func (v Value) IsNumber() bool { return v.Kind == KInt || v.Kind == KFloat }

// IsProcedure reports KProc or KPrim.
func (v Value) IsProcedure() bool { return v.Kind == KProc || v.Kind == KPrim }

// AsFloat converts a number to float64.
func (v Value) AsFloat() float64 {
	if v.Kind == KInt {
		return float64(v.I)
	}
	return v.F
}

// Slice converts a proper list to a slice. ok is false for improper lists.
func (v Value) Slice() (out []Value, ok bool) {
	for v.Kind == KPair {
		out = append(out, v.P.Car)
		v = v.P.Cdr
	}
	return out, v.Kind == KNil
}

// IsList reports whether v is a proper list.
func (v Value) IsList() bool {
	for v.Kind == KPair {
		v = v.P.Cdr
	}
	return v.Kind == KNil
}

// Eqv implements eq?/eqv?: identity for pairs and procedures, value for
// everything else.
func Eqv(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KNil, KVoid:
		return true
	case KBool, KInt:
		return a.I == b.I
	case KFloat:
		return a.F == b.F
	case KString, KSymbol:
		return a.S == b.S
	case KPair:
		return a.P == b.P
	case KProc:
		return a.Proc == b.Proc
	case KPrim:
		return a.Prim == b.Prim
	case KHandle:
		return a.H == b.H
	case KVec:
		return a.V == b.V
	case KHash:
		return a.Hash == b.Hash
	}
	return false
}

// Equal implements equal?: structural equality. Values nested past MaxDepth
// are not equal unless they are the same object, so two vectors that hold
// themselves compare without end only in Scheme's specification.
func Equal(a, b Value) bool { return equal(a, b, 0) }

func equal(a, b Value, depth int) bool {
	for {
		if depth > MaxDepth {
			return Eqv(a, b)
		}
		switch {
		case a.Kind == KPair && b.Kind == KPair:
			if !equal(a.P.Car, b.P.Car, depth+1) {
				return false
			}
			a, b = a.P.Cdr, b.P.Cdr
		case a.Kind == KVec && b.Kind == KVec:
			if a.V == b.V {
				return true
			}
			// One element at a time, each under its own lock and never
			// both vectors' at once: the vectors may be changing, and a
			// pair of locks taken in two orders would deadlock.
			n := a.V.Len()
			if n != b.V.Len() {
				return false
			}
			for i := 0; i < n; i++ {
				x, okx := a.V.At(i)
				y, oky := b.V.At(i)
				if !okx || !oky || !equal(x, y, depth+1) {
					return false
				}
			}
			return true
		default:
			return Eqv(a, b)
		}
	}
}

// Write renders v in machine form (strings quoted), as the reader reads it.
func Write(v Value) string {
	var sb strings.Builder
	print(&sb, v, true, nil, 0, &printState{})
	return sb.String()
}

// Display renders v in human form (strings as their text).
func Display(v Value) string {
	var sb strings.Builder
	print(&sb, v, false, nil, 0, &printState{})
	return sb.String()
}

// WriteWithStrings renders v like Write, transforming each string before quoting.
// It also visits strings in pairs and vectors, without changing the value.
func WriteWithStrings(v Value, transform func(string) string) string {
	var sb strings.Builder
	print(&sb, v, true, transform, 0, &printState{})
	return sb.String()
}

// String implements fmt.Stringer as Write.
func (v Value) String() string { return Write(v) }

// printState carries what printing needs to remember between nested values:
// the hash tables being written, so one that holds itself prints as #<hash ...>
// at the inner level and does not branch into every path through it.
type printState struct {
	hashes map[*Hash]bool
}

func printHash(sb *strings.Builder, h *Hash, write bool, transform func(string) string, depth int, st *printState) {
	if st.hashes[h] {
		sb.WriteString("#<hash ...>")
		return
	}
	if st.hashes == nil {
		st.hashes = map[*Hash]bool{}
	}
	st.hashes[h] = true
	defer delete(st.hashes, h)
	sb.WriteString("#<hash")
	h.each(func(k, v Value) {
		sb.WriteString(" (")
		print(sb, k, write, transform, depth+1, st)
		sb.WriteString(" . ")
		print(sb, v, write, transform, depth+1, st)
		sb.WriteByte(')')
	})
	sb.WriteByte('>')
}

// print writes v at the given nesting depth. Past MaxDepth it writes "..."
// instead: a vector can hold itself (vector-set!), and printing it would
// otherwise never end.
func print(sb *strings.Builder, v Value, write bool, transform func(string) string, depth int, st *printState) {
	if depth > MaxDepth {
		sb.WriteString("...")
		return
	}
	switch v.Kind {
	case KNil:
		sb.WriteString("()")
	case KBool:
		if v.I != 0 {
			sb.WriteString("#t")
		} else {
			sb.WriteString("#f")
		}
	case KInt:
		sb.WriteString(strconv.FormatInt(v.I, 10))
	case KFloat:
		sb.WriteString(formatFloat(v.F))
	case KString:
		if transform != nil {
			v.S = transform(v.S)
		}
		if write {
			quote(sb, v.S)
		} else {
			sb.WriteString(v.S)
		}
	case KSymbol:
		sb.WriteString(v.S)
	case KPair:
		sb.WriteByte('(')
		first := true
		for v.Kind == KPair {
			if !first {
				sb.WriteByte(' ')
			}
			first = false
			print(sb, v.P.Car, write, transform, depth+1, st)
			v = v.P.Cdr
		}
		if v.Kind != KNil {
			sb.WriteString(" . ")
			print(sb, v, write, transform, depth+1, st)
		}
		sb.WriteByte(')')
	case KProc:
		if name := v.Proc.DisplayName(); name != "" {
			fmt.Fprintf(sb, "#<procedure %s>", name)
		} else {
			sb.WriteString("#<procedure>")
		}
	case KPrim:
		fmt.Fprintf(sb, "#<primitive %s>", v.Prim.Name)
	case KHandle:
		fmt.Fprintf(sb, "#<cred:%s:%s>", v.H.Class, v.H.ID)
	case KVec:
		sb.WriteString("#(")
		for i, el := range v.V.Elements() {
			if i > 0 {
				sb.WriteByte(' ')
			}
			print(sb, el, write, transform, depth+1, st)
		}
		sb.WriteByte(')')
	case KHash:
		printHash(sb, v.Hash, write, transform, depth, st)
	case KVoid:
		sb.WriteString("#<void>")
	default:
		sb.WriteString("#<unknown>")
	}
}

// quote writes s in the escaped form the reader accepts (docs/scheme.md §1):
// \" \\ \n \t \r, and \xHH for any other control or invalid byte.
func quote(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(sb, `\x%02x`, c)
				i++
				continue
			}
			if c < utf8.RuneSelf {
				sb.WriteByte(c)
				i++
				continue
			}
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				fmt.Fprintf(sb, `\x%02x`, c)
			} else {
				sb.WriteString(s[i : i+n])
			}
			i += n
			continue
		}
		i++
	}
	sb.WriteByte('"')
}

func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eIN") { // no dot, no exponent, not Inf/NaN
		s += ".0"
	}
	return s
}
