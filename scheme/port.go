package scheme

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// eofObject is what reading at the end of input gives.
type eofObject struct{}

func (*eofObject) Kind() string   { return "end-of-file object" }
func (*eofObject) String() string { return "#<eof>" }

var eofObj = &eofObject{}

// EOF is the value reading gives at the end of input; eof-object? is true for it.
func EOF() Value { return ObjectValue(eofObj) }

// port is a textual port: input from a string or from the interpreter's input,
// or output into a string or to wherever display goes. Tasks may share one, so
// every operation holds its lock.
type port struct {
	mu sync.Mutex

	input, output bool
	closed        bool

	// Input. data is text taken from the source and not yet read; more
	// gets the next piece of the source, or says there is none.
	data string
	more func() (string, bool)

	// Output. cur is the current output port, which has no text of its own:
	// it writes where display writes. Any other keeps what it is given.
	cur   bool
	sb    strings.Builder
	taint Taint
}

func (p *port) Kind() string { return "port" }

func (p *port) String() string {
	switch {
	case p.cur:
		return "#<output-port>"
	case p.input:
		return "#<input-port>"
	}
	return "#<output-port string>"
}

// curOut is the current output port: one for all interpreters, as it holds no
// state; a write resolves to the writer of the task that makes it.
var curOut = &port{output: true, cur: true}

// inputPort is a port that reads from r, a line at a time.
func inputPort(r io.Reader) *port {
	br := bufio.NewReader(r)
	return &port{input: true, more: func() (string, bool) {
		s, _ := br.ReadString('\n')
		return s, s != ""
	}}
}

// WithInput sets where the current input port, and so read-line, read-char and
// read with no port, get their text. Default: none, so they find the end of
// input at once. The host decides who reads its standard input.
func WithInput(r io.Reader) Option {
	return func(in *Interp) { in.input = inputPort(r) }
}

// fill makes sure p.data has text if the source has any.
func (p *port) fill() bool {
	for p.data == "" {
		if p.more == nil {
			return false
		}
		s, ok := p.more()
		if !ok {
			p.more = nil
			return false
		}
		p.data = s
	}
	return true
}

func (p *port) readRune() (rune, bool) {
	if !p.fill() {
		return 0, false
	}
	r, n := utf8.DecodeRuneInString(p.data)
	p.data = p.data[n:]
	return r, true
}

func (p *port) peekRune() (rune, bool) {
	if !p.fill() {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(p.data)
	return r, true
}

// readLine is the next line without its ending, or false at the end of input.
func (p *port) readLine() (string, bool) {
	var sb strings.Builder
	got := false
	for p.fill() {
		got = true
		if i := strings.IndexByte(p.data, '\n'); i >= 0 {
			sb.WriteString(p.data[:i])
			p.data = p.data[i+1:]
			return strings.TrimSuffix(sb.String(), "\r"), true
		}
		sb.WriteString(p.data)
		p.data = ""
	}
	if !got {
		return "", false
	}
	return strings.TrimSuffix(sb.String(), "\r"), true
}

// errNoDatum says there was nothing but white space and comments left to read.
var errNoDatum = errors.New("no datum")

// readDatum parses the next datum from the port.
func (p *port) readDatum() (Value, error) {
	for {
		v, n, err := readPrefix(p.data, p.more == nil)
		switch {
		case err == nil:
			p.data = p.data[n:]
			return v, nil
		case errors.Is(err, errNoDatum), errors.Is(err, ErrIncomplete), errors.Is(err, errMaybeMore):
			// More text may complete it.
			more, ok := "", false
			if p.more != nil {
				more, ok = p.more()
			}
			if !ok {
				p.more = nil
				switch {
				case errors.Is(err, errNoDatum):
					p.data = ""
					return EOF(), nil
				case errors.Is(err, errMaybeMore):
					continue // there is no more: the datum is all there is
				}
				return Value{}, errors.New("end of input inside a datum")
			}
			p.data += more
		default:
			return Value{}, err
		}
	}
}

// write adds s to an output port. The current output port writes where display
// does; its taint is what with-output-to-string will give the text.
func (p *port) write(c *Call, s string, taint Taint) error {
	if p.cur {
		c.t.printed = Union(c.t.printed, taint)
		_, err := io.WriteString(c.t.writer(c.In), s)
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errorf(c.Name, "the port is closed")
	}
	p.sb.WriteString(s)
	p.taint = Union(p.taint, taint)
	return nil
}

// inPort is argument i as an input port, or the current input port if the call
// stops short of it.
func (c *Call) inPort(i int) (*port, error) {
	if len(c.Args) <= i {
		return c.In.input, nil
	}
	p, err := objArg[*port](c, i, "port")
	if err != nil {
		return nil, err
	}
	if !p.input {
		return nil, c.typeErr(i, "input port")
	}
	return p, nil
}

// outPort is argument i as an output port, or the current output port.
func (c *Call) outPort(i int) (*port, error) {
	if len(c.Args) <= i {
		return curOut, nil
	}
	p, err := objArg[*port](c, i, "port")
	if err != nil {
		return nil, err
	}
	if !p.output {
		return nil, c.typeErr(i, "output port")
	}
	return p, nil
}

func (in *Interp) registerPorts() {
	any1 := []Param{{Name: "x", Type: "any"}}
	optPort := Param{Name: "port", Type: "any", Optional: true}

	in.mustRegisterOpaque("port?", "True for ports.", any1, false, func(c *Call) (Value, error) {
		_, ok := ObjectOf(c.Args[0]).(*port)
		return Bool(ok), nil
	})
	in.mustRegisterOpaque("input-port?", "True for ports that can be read from.", any1, false, func(c *Call) (Value, error) {
		p, ok := ObjectOf(c.Args[0]).(*port)
		return Bool(ok && p.input), nil
	})
	in.mustRegisterOpaque("output-port?", "True for ports that can be written to.", any1, false, func(c *Call) (Value, error) {
		p, ok := ObjectOf(c.Args[0]).(*port)
		return Bool(ok && p.output), nil
	})
	in.mustRegisterOpaque("eof-object?", "True for the value reading gives at the end of input.", any1, false, func(c *Call) (Value, error) {
		return Bool(ObjectOf(c.Args[0]) == Object(eofObj)), nil
	})
	in.mustRegister("eof-object", "The value reading gives at the end of input.", nil, false, func(c *Call) (Value, error) {
		return EOF(), nil
	})
	in.mustRegister("current-input-port", "The port read-line, read-char and read use when they are not given one.", nil, false, func(c *Call) (Value, error) {
		return ObjectValue(c.In.input), nil
	})
	in.mustRegister("current-output-port", "The port display, write and newline use when they are not given one.", nil, false, func(c *Call) (Value, error) {
		return ObjectValue(curOut), nil
	})
	in.mustRegister("open-input-string", "A port that reads the characters of a string.", []Param{{Name: "s", Type: "string"}}, false, func(c *Call) (Value, error) {
		s, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		return ObjectValue(&port{input: true, data: s}), nil
	})
	in.mustRegister("open-output-string", "A port that collects what is written to it; get-output-string returns it.", nil, false, func(c *Call) (Value, error) {
		return ObjectValue(&port{output: true}), nil
	})
	in.mustRegister("get-output-string", "The text written to a string port so far.", []Param{{Name: "port", Type: "any"}}, false, func(c *Call) (Value, error) {
		p, err := objArg[*port](c, 0, "port")
		if err != nil {
			return Value{}, err
		}
		if !p.output || p.cur {
			return Value{}, c.typeErr(0, "string output port")
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return Value{Kind: KString, S: p.sb.String(), Taint: p.taint}, nil
	})
	in.mustRegister("call-with-output-string", "Call a procedure with a string port, and return the text it wrote to it.", []Param{{Name: "proc", Type: "any"}}, false, func(c *Call) (Value, error) {
		f, err := c.Proc(0)
		if err != nil {
			return Value{}, err
		}
		p := &port{output: true}
		if _, err := c.In.Apply(c.Ctx, f, []Value{ObjectValue(p)}); err != nil {
			return Value{}, err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return Value{Kind: KString, S: p.sb.String(), Taint: p.taint}, nil
	})
	closer := func(name, doc string) {
		in.mustRegister(name, doc, []Param{{Name: "port", Type: "any"}}, false, func(c *Call) (Value, error) {
			p, err := objArg[*port](c, 0, "port")
			if err != nil {
				return Value{}, err
			}
			p.mu.Lock()
			p.closed = true
			p.mu.Unlock()
			return Void, nil
		})
	}
	closer("close-port", "Close a port: writing to a closed string port is an error, and reading from one finds the end.")
	closer("close-input-port", "Close an input port.")
	closer("close-output-port", "Close an output port.")

	// Reading. A closed port is at its end.
	read := func(c *Call, i int, f func(p *port) (Value, error)) (Value, error) {
		p, err := c.inPort(i)
		if err != nil {
			return Value{}, err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.closed {
			return EOF(), nil
		}
		return f(p)
	}
	in.mustRegister("read-char", "The next character from a port, or the end-of-file object.", []Param{optPort}, false, func(c *Call) (Value, error) {
		return read(c, 0, func(p *port) (Value, error) {
			if r, ok := p.readRune(); ok {
				return Char(r), nil
			}
			return EOF(), nil
		})
	})
	in.mustRegister("peek-char", "The next character from a port without taking it, or the end-of-file object.", []Param{optPort}, false, func(c *Call) (Value, error) {
		return read(c, 0, func(p *port) (Value, error) {
			if r, ok := p.peekRune(); ok {
				return Char(r), nil
			}
			return EOF(), nil
		})
	})
	in.mustRegister("read-line", "The next line from a port without its line ending, or the end-of-file object.", []Param{optPort}, false, func(c *Call) (Value, error) {
		return read(c, 0, func(p *port) (Value, error) {
			if s, ok := p.readLine(); ok {
				return Str(s), nil
			}
			return EOF(), nil
		})
	})
	in.mustRegister("read-string", "The next k characters from a port as a string (fewer at the end), or the end-of-file object if there are none.", []Param{{Name: "k", Type: "integer"}, optPort}, false, func(c *Call) (Value, error) {
		k, err := c.Int(0)
		if err != nil {
			return Value{}, err
		}
		if k < 0 {
			return Value{}, errorf(c.Name, "%d is not a count", k)
		}
		return read(c, 1, func(p *port) (Value, error) {
			var sb strings.Builder
			for n := int64(0); n < k; n++ {
				r, ok := p.readRune()
				if !ok {
					break
				}
				sb.WriteRune(r)
			}
			if sb.Len() == 0 && k > 0 {
				return EOF(), nil
			}
			return Str(sb.String()), nil
		})
	})
	in.mustRegister("read", "Parse the next datum from a port, the way the reader would, or give the end-of-file object. Nothing is evaluated.", []Param{optPort}, false, func(c *Call) (Value, error) {
		return read(c, 0, func(p *port) (Value, error) {
			v, err := p.readDatum()
			if err != nil {
				return Value{}, errorf(c.Name, "%v", err)
			}
			return v, nil
		})
	})

	// Writing.
	in.mustRegister("write-string", "Write a string to a port, or to the current output.", []Param{{Name: "s", Type: "string"}, optPort}, false, func(c *Call) (Value, error) {
		s, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		p, err := c.outPort(1)
		if err != nil {
			return Value{}, err
		}
		return Void, p.write(c, s, c.Args[0].Taint)
	})
	in.mustRegister("write-char", "Write a character to a port, or to the current output.", []Param{{Name: "c", Type: "any"}, optPort}, false, func(c *Call) (Value, error) {
		r, err := c.Char(0)
		if err != nil {
			return Value{}, err
		}
		p, err := c.outPort(1)
		if err != nil {
			return Value{}, err
		}
		return Void, p.write(c, string(r), c.Args[0].Taint)
	})
}

// errMaybeMore says the datum ran to the end of the text, so the next piece may
// continue it.
var errMaybeMore = errors.New("datum may continue")

// readPrefix parses the first datum of src, and says how many bytes it took.
// errNoDatum means src holds nothing but white space and comments, and
// ErrIncomplete that it ends inside a datum. final says src is all there is;
// otherwise a datum that runs to the end of src is errMaybeMore.
func readPrefix(src string, final bool) (Value, int, error) {
	r := &reader{src: src, line: 1, col: 1}
	r.skipSpace()
	if r.unterminated {
		return Value{}, 0, ErrIncomplete
	}
	if r.eof() {
		return Value{}, 0, errNoDatum
	}
	v, err := r.datum()
	if err != nil {
		return Value{}, 0, err
	}
	// A symbol or number that runs to the end of the text may go on in the next
	// piece: the reader only knows it has all of it at a delimiter.
	if !final && r.eof() && endsInAtom(src) {
		return Value{}, 0, errMaybeMore
	}
	return v, r.pos, nil
}

// endsInAtom reports whether src ends in a character that could be the middle of
// a symbol, number or character name.
func endsInAtom(src string) bool {
	c, _ := utf8.DecodeLastRuneInString(src)
	return !isDelimiter(c)
}
