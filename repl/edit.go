package repl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

// ErrInterrupt is what ReadLine returns when the person pressed Ctrl-C at the
// prompt: the line they were typing is dropped and the loop asks for another.
var ErrInterrupt = errors.New("interrupted")

// LineReader gives the loop its lines. Editor is one that edits, completes and
// remembers; without one, Run reads lines from its input as they come.
type LineReader interface {
	// ReadLine shows prompt and returns the line typed, without the newline.
	// It returns io.EOF when there is no more input and ErrInterrupt for Ctrl-C.
	ReadLine(prompt string) (string, error)
	// AddHistory records a line that was accepted.
	AddHistory(line string)
}

// Editor reads a line from a terminal with the keys people expect: the arrows
// and Ctrl-A, E, B, F, K, U, W, D, L and T to move and change, Up and Down for
// the lines before, Tab to complete and Ctrl-C to start over.
//
// The terminal modes are set by Raw, which is set by NewTerminalEditor; an
// Editor that has none (as in a test) reads the same bytes from any reader.
type Editor struct {
	// Complete returns the candidates for the word that ends at pos in line,
	// and the index in line where that word starts. Nil means Tab does nothing.
	Complete func(line []rune, pos int) (start int, candidates []string)
	// Raw puts the terminal in the mode ReadLine needs and returns what puts it
	// back. Nil leaves the terminal as it is.
	Raw func() (restore func(), err error)
	// Width is how many columns the terminal has; nil means 80.
	Width func() int
	// HistoryFile, if set, is where lines are kept between sessions.
	HistoryFile string

	r       *bufio.Reader
	w       io.Writer
	history []string
}

// NewEditor reads keys from r and writes to w.
func NewEditor(r io.Reader, w io.Writer) *Editor {
	return &Editor{r: bufio.NewReader(r), w: w}
}

// LoadHistory reads the lines kept in HistoryFile. A missing file is no history.
func (e *Editor) LoadHistory() {
	if e.HistoryFile == "" {
		return
	}
	data, err := os.ReadFile(e.HistoryFile)
	if err != nil {
		return
	}
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			e.history = append(e.history, strings.ReplaceAll(l, `\n`, "\n"))
		}
	}
	const keep = 1000
	if len(e.history) > keep {
		e.history = e.history[len(e.history)-keep:]
	}
}

// AddHistory records line, unless it is empty or the same as the last one.
func (e *Editor) AddHistory(line string) {
	if strings.TrimSpace(line) == "" || (len(e.history) > 0 && e.history[len(e.history)-1] == line) {
		return
	}
	e.history = append(e.history, line)
	if e.HistoryFile == "" {
		return
	}
	f, err := os.OpenFile(e.HistoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, strings.ReplaceAll(line, "\n", `\n`))
}

// History is the lines remembered, oldest first.
func (e *Editor) History() []string { return append([]string(nil), e.history...) }

// lineState is one ReadLine's work: the text, the cursor in it, and where in the
// history the person has gone.
type lineState struct {
	prompt string
	buf    []rune
	pos    int
	hist   int    // index into history while browsing; len(history) when not
	saved  string // the line being typed, kept while browsing
	killed []rune // what Ctrl-K, Ctrl-U or Ctrl-W last removed, for Ctrl-Y
	tabbed bool   // the last key was Tab
}

// ReadLine shows prompt and edits a line until Enter.
func (e *Editor) ReadLine(prompt string) (string, error) {
	if e.Raw != nil {
		restore, err := e.Raw()
		if err != nil {
			return "", err
		}
		defer restore()
	}
	s := &lineState{prompt: prompt, hist: len(e.history)}
	e.refresh(s)
	for {
		r, _, err := e.r.ReadRune()
		if err != nil {
			if err == io.EOF && len(s.buf) > 0 {
				break // input ended in the middle of a line: take what there is
			}
			return "", err
		}
		wasTab := s.tabbed
		s.tabbed = false
		switch r {
		case '\r', '\n':
			// A line ending with \r\n is one Enter.
			if r == '\r' && e.r.Buffered() > 0 {
				if next, _ := e.r.Peek(1); len(next) == 1 && next[0] == '\n' {
					e.r.ReadByte()
				}
			}
			e.moveToEnd(s)
			fmt.Fprint(e.w, "\r\n")
			return string(s.buf), nil
		case 3: // Ctrl-C
			fmt.Fprint(e.w, "^C\r\n")
			return "", ErrInterrupt
		case 4: // Ctrl-D
			if len(s.buf) == 0 {
				return "", io.EOF
			}
			e.deleteAt(s)
		case 1: // Ctrl-A
			s.pos = 0
		case 5: // Ctrl-E
			s.pos = len(s.buf)
		case 2: // Ctrl-B
			e.left(s)
		case 6: // Ctrl-F
			e.right(s)
		case 11: // Ctrl-K
			s.killed = append([]rune(nil), s.buf[s.pos:]...)
			s.buf = s.buf[:s.pos]
		case 21: // Ctrl-U
			s.killed = append([]rune(nil), s.buf[:s.pos]...)
			s.buf = append([]rune(nil), s.buf[s.pos:]...)
			s.pos = 0
		case 23: // Ctrl-W
			e.killWordBack(s)
		case 25: // Ctrl-Y
			e.insert(s, s.killed...)
		case 20: // Ctrl-T
			if s.pos > 0 && len(s.buf) > 1 {
				if s.pos == len(s.buf) {
					s.pos--
				}
				s.buf[s.pos-1], s.buf[s.pos] = s.buf[s.pos], s.buf[s.pos-1]
				s.pos++
			}
		case 12: // Ctrl-L
			fmt.Fprint(e.w, "\x1b[H\x1b[2J")
		case 127, 8: // Backspace
			if s.pos > 0 {
				s.buf = append(s.buf[:s.pos-1], s.buf[s.pos:]...)
				s.pos--
			}
		case 16: // Ctrl-P
			e.older(s)
		case 14: // Ctrl-N
			e.newer(s)
		case '\t':
			e.complete(s, wasTab)
			s.tabbed = true
		case 27:
			e.escape(s)
		default:
			if r >= ' ' && !unicode.IsControl(r) {
				e.insert(s, r)
			}
		}
		e.refresh(s)
	}
	fmt.Fprint(e.w, "\r\n")
	return string(s.buf), nil
}

func (e *Editor) insert(s *lineState, rs ...rune) {
	if len(rs) == 0 {
		return
	}
	tail := append([]rune(nil), s.buf[s.pos:]...)
	s.buf = append(append(s.buf[:s.pos], rs...), tail...)
	s.pos += len(rs)
}

func (e *Editor) deleteAt(s *lineState) {
	if s.pos < len(s.buf) {
		s.buf = append(s.buf[:s.pos], s.buf[s.pos+1:]...)
	}
}

func (e *Editor) left(s *lineState) {
	if s.pos > 0 {
		s.pos--
	}
}

func (e *Editor) right(s *lineState) {
	if s.pos < len(s.buf) {
		s.pos++
	}
}

func isWordRune(r rune) bool {
	return !unicode.IsSpace(r) && r != '(' && r != ')' && r != '\'' && r != '"'
}

func (e *Editor) wordLeft(s *lineState) {
	for s.pos > 0 && !isWordRune(s.buf[s.pos-1]) {
		s.pos--
	}
	for s.pos > 0 && isWordRune(s.buf[s.pos-1]) {
		s.pos--
	}
}

func (e *Editor) wordRight(s *lineState) {
	for s.pos < len(s.buf) && !isWordRune(s.buf[s.pos]) {
		s.pos++
	}
	for s.pos < len(s.buf) && isWordRune(s.buf[s.pos]) {
		s.pos++
	}
}

func (e *Editor) killWordBack(s *lineState) {
	end := s.pos
	e.wordLeft(s)
	s.killed = append([]rune(nil), s.buf[s.pos:end]...)
	s.buf = append(s.buf[:s.pos], s.buf[end:]...)
}

func (e *Editor) setLine(s *lineState, text string) {
	s.buf = []rune(text)
	s.pos = len(s.buf)
}

func (e *Editor) older(s *lineState) {
	if s.hist == 0 {
		return
	}
	if s.hist == len(e.history) {
		s.saved = string(s.buf)
	}
	s.hist--
	e.setLine(s, e.history[s.hist])
}

func (e *Editor) newer(s *lineState) {
	if s.hist >= len(e.history) {
		return
	}
	s.hist++
	if s.hist == len(e.history) {
		e.setLine(s, s.saved)
		return
	}
	e.setLine(s, e.history[s.hist])
}

// escape reads what follows an Escape: an arrow, Home, End, Delete, or a
// word move. A key it does not know is dropped whole.
func (e *Editor) escape(s *lineState) {
	if e.r.Buffered() == 0 {
		return // Escape by itself
	}
	r, _, _ := e.r.ReadRune()
	switch r {
	case 'b':
		e.wordLeft(s)
	case 'f':
		e.wordRight(s)
	case 'd':
		start := s.pos
		e.wordRight(s)
		end := s.pos
		s.pos = start
		s.buf = append(s.buf[:start], s.buf[end:]...)
	case 'O':
		k, _, _ := e.r.ReadRune()
		switch k {
		case 'H':
			s.pos = 0
		case 'F':
			s.pos = len(s.buf)
		}
	case '[':
		var params []rune
		var final rune
		for {
			k, _, err := e.r.ReadRune()
			if err != nil {
				return
			}
			if k >= 0x40 && k <= 0x7e {
				final = k
				break
			}
			params = append(params, k)
		}
		p := string(params)
		switch final {
		case 'A':
			e.older(s)
		case 'B':
			e.newer(s)
		case 'C':
			if strings.HasSuffix(p, ";5") || strings.HasSuffix(p, ";3") {
				e.wordRight(s)
			} else {
				e.right(s)
			}
		case 'D':
			if strings.HasSuffix(p, ";5") || strings.HasSuffix(p, ";3") {
				e.wordLeft(s)
			} else {
				e.left(s)
			}
		case 'H':
			s.pos = 0
		case 'F':
			s.pos = len(s.buf)
		case '~':
			switch p {
			case "1", "7":
				s.pos = 0
			case "4", "8":
				s.pos = len(s.buf)
			case "3":
				e.deleteAt(s)
			}
		}
	}
}

// complete finishes the word before the cursor. It adds what all the
// candidates share; when there is nothing to add, a second Tab lists them.
func (e *Editor) complete(s *lineState, again bool) {
	if e.Complete == nil {
		return
	}
	start, cands := e.Complete(s.buf, s.pos)
	if len(cands) == 0 {
		fmt.Fprint(e.w, "\a")
		return
	}
	prefix := string(s.buf[start:s.pos])
	common := commonPrefix(cands)
	if len(cands) == 1 {
		common += " "
		if strings.HasPrefix(cands[0], ",") {
			common = cands[0] + " "
		}
	}
	if len([]rune(common)) > len([]rune(prefix)) {
		s.buf = append(append(append([]rune(nil), s.buf[:start]...), []rune(common)...), s.buf[s.pos:]...)
		s.pos = start + len([]rune(common))
		return
	}
	if !again {
		fmt.Fprint(e.w, "\a")
		return
	}
	fmt.Fprint(e.w, "\r\n"+columns(cands, e.width())+"\r\n")
}

func commonPrefix(ss []string) string {
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

// columns lays words out in rows that fit width, each row ending in \r\n.
func columns(words []string, width int) string {
	longest := 0
	for _, w := range words {
		if n := len([]rune(w)); n > longest {
			longest = n
		}
	}
	colw := longest + 2
	per := width / colw
	if per < 1 {
		per = 1
	}
	var sb strings.Builder
	for i, w := range words {
		sb.WriteString(w)
		if (i+1)%per == 0 || i == len(words)-1 {
			if i != len(words)-1 {
				sb.WriteString("\r\n")
			}
			continue
		}
		sb.WriteString(strings.Repeat(" ", colw-len([]rune(w))))
	}
	return sb.String()
}

func (e *Editor) width() int {
	if e.Width != nil {
		if w := e.Width(); w > 10 {
			return w
		}
	}
	return 80
}

func (e *Editor) moveToEnd(s *lineState) {
	s.pos = len(s.buf)
	e.refresh(s)
}

// refresh draws the prompt and the line, scrolled sideways when it is wider
// than the terminal, and puts the cursor where it belongs.
func (e *Editor) refresh(s *lineState) {
	plen := len([]rune(s.prompt))
	avail := e.width() - plen - 1
	if avail < 8 {
		avail = 8
	}
	start := 0
	if s.pos >= avail {
		start = s.pos - avail + 1
	}
	end := start + avail
	if end > len(s.buf) {
		end = len(s.buf)
	}
	var sb strings.Builder
	sb.WriteString("\r" + s.prompt + string(s.buf[start:end]) + "\x1b[K\r")
	if col := plen + s.pos - start; col > 0 {
		fmt.Fprintf(&sb, "\x1b[%dC", col)
	}
	io.WriteString(e.w, sb.String())
}

// NewTerminalEditor is an Editor for the terminal on in and out (the standard
// input and output of a person at a prompt), or nil when this system cannot
// edit lines in place or the terminal does not understand the codes it writes
// (TERM=dumb). Complete may be nil. The history is kept in historyFile if it
// is not empty.
func NewTerminalEditor(in, out *os.File, complete func([]rune, int) (int, []string), historyFile string) *Editor {
	if !editorSupported || os.Getenv("TERM") == "dumb" {
		return nil
	}
	e := NewEditor(in, out)
	e.Complete = complete
	e.HistoryFile = historyFile
	e.Raw = func() (func(), error) { return rawMode(in) }
	e.Width = func() int { return termWidth(out) }
	e.LoadHistory()
	return e
}
