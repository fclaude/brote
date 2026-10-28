package scheme

// Syntax is a source-preserving datum. Start and End are UTF-8 byte offsets;
// End is exclusive. Prefix holds quote, quasiquote, unquote or unquote-splicing.
// Children preserves lists (including a dotted tail) and vectors. Atom is set
// only for identifiers. Incomplete lists still have a node for editor tooling.
type Syntax struct {
	Start, End int
	Atom       string
	Prefix     string
	Children   []*Syntax
}

// ReadSyntax reads without evaluation. It returns partial syntax even when Read
// reports an error, allowing tools to work on unfinished input. Validation uses
// the interpreter's reader, so tooling accepts precisely the same language.
func ReadSyntax(src string) ([]*Syntax, error) {
	_, err := Read(src)
	r := &reader{src: src, line: 1, col: 1}
	var nodes []*Syntax
	for {
		r.skipSpace()
		if r.eof() {
			break
		}
		before := r.pos
		nodes = append(nodes, r.syntax(0))
		if before == r.pos {
			r.next()
		}
	}
	return nodes, err
}

func (r *reader) syntax(depth int) *Syntax {
	r.skipSpace()
	n := &Syntax{Start: r.pos}
	defer func() { n.End = r.pos }()
	if r.eof() {
		return n
	}
	if depth >= MaxDepth {
		r.pos = len(r.src)
		return n
	}
	switch r.peek() {
	case '\'', '`', ',':
		c := r.next()
		n.Prefix = map[rune]string{'\'': "quote", '`': "quasiquote", ',': "unquote"}[c]
		if c == ',' && r.peek() == '@' {
			r.next()
			n.Prefix = "unquote-splicing"
		}
		if !r.eof() {
			n.Children = append(n.Children, r.syntax(depth+1))
		}
		return n
	case '(':
		r.next()
		for {
			r.skipSpace()
			if r.eof() {
				return n
			}
			if r.peek() == ')' {
				r.next()
				return n
			}
			n.Children = append(n.Children, r.syntax(depth+1))
		}
	case ')':
		r.next()
		return n
	default:
		if r.startsWith("#(") {
			r.next()
			child := r.syntax(depth + 1)
			n.Prefix = "vector"
			n.Children = child.Children
			return n
		}
		before := r.pos
		v, err := r.datum()
		if r.pos == before && !r.eof() {
			r.next()
		}
		if err == nil && v.Kind == KSymbol {
			n.Atom = v.S
		}
		return n
	}
}
