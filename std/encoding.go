package std

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Encoding] = installEncoding }

// maxCSV bounds the rows and cells csv-parse returns; a script that needs more
// reads the file in pieces.
const maxCSVCells = 1_000_000

func installEncoding(s *installer) error {
	text := []scheme.Param{{Name: "text", Type: "string"}}
	urlFlag := scheme.Param{Name: "url-safe", Type: "boolean", Doc: "use the URL alphabet (- and _) and no padding", Optional: true}

	textFn := func(f func(string) (string, error)) func(*scheme.Call) (scheme.Value, error) {
		return func(c *scheme.Call) (scheme.Value, error) {
			in, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			out, err := f(in)
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.Str(out), nil
		}
	}
	b64 := func(c *scheme.Call) *base64.Encoding {
		if len(c.Args) > 1 && c.Args[1].IsTrue() {
			return base64.RawURLEncoding
		}
		return base64.StdEncoding
	}
	s.add(&scheme.Prim{
		Name: "base64-encode",
		Meta: scheme.Meta{Doc: "The Base64 text of a string's UTF-8 bytes. With #t as the second argument, the URL-safe alphabet without padding.", Params: append(text[:1:1], urlFlag)},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			in, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.Str(b64(c).EncodeToString([]byte(in))), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "base64-decode",
		Meta: scheme.Meta{Doc: "The string that Base64 text stands for. An error if the text is not Base64, or decodes to bytes that are not UTF-8 text (strings are text).", Params: append(text[:1:1], urlFlag)},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			in, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			b, err := b64(c).DecodeString(strings.TrimSpace(in))
			if err != nil {
				return scheme.Value{}, fmt.Errorf("not valid base64: %w", err)
			}
			if !utf8.Valid(b) {
				return scheme.Value{}, fmt.Errorf("the bytes are not UTF-8 text, and a string cannot hold them")
			}
			return scheme.Str(string(b)), nil
		},
	})
	s.add(&scheme.Prim{
		Name:    "hex-encode",
		Meta:    scheme.Meta{Doc: "The lower-case hexadecimal text of a string's UTF-8 bytes.", Params: text},
		Handler: textFn(func(in string) (string, error) { return hex.EncodeToString([]byte(in)), nil }),
	})
	s.add(&scheme.Prim{
		Name: "hex-decode",
		Meta: scheme.Meta{Doc: "The string that hexadecimal text stands for. An error if it is not hexadecimal or its bytes are not UTF-8 text.", Params: text},
		Handler: textFn(func(in string) (string, error) {
			b, err := hex.DecodeString(strings.TrimSpace(in))
			if err != nil {
				return "", fmt.Errorf("not valid hex: %w", err)
			}
			if !utf8.Valid(b) {
				return "", fmt.Errorf("the bytes are not UTF-8 text, and a string cannot hold them")
			}
			return string(b), nil
		}),
	})
	s.add(&scheme.Prim{
		Name:    "url-encode",
		Meta:    scheme.Meta{Doc: "Escape text for a URL query value (space becomes +, reserved characters %XX).", Params: text},
		Handler: textFn(func(in string) (string, error) { return url.QueryEscape(in), nil }),
	})
	s.add(&scheme.Prim{
		Name: "url-decode",
		Meta: scheme.Meta{Doc: "Undo url-encode. An error for a bad % escape.", Params: text},
		Handler: textFn(func(in string) (string, error) {
			out, err := url.QueryUnescape(in)
			if err != nil {
				return "", fmt.Errorf("bad escape: %w", err)
			}
			return out, nil
		}),
	})
	s.add(&scheme.Prim{
		Name: "csv-parse",
		Meta: scheme.Meta{
			Doc: "Parse CSV text (RFC 4180) into a list of rows, each a list of strings. The optional delimiter is a one-character string, \",\" by default.",
			Params: []scheme.Param{
				{Name: "text", Type: "string"},
				{Name: "delimiter", Type: "string", Optional: true},
			},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			in, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			r := csv.NewReader(strings.NewReader(in))
			r.FieldsPerRecord = -1
			r.LazyQuotes = true
			if len(c.Args) > 1 {
				d, err := c.String(1)
				if err != nil {
					return scheme.Value{}, err
				}
				if utf8.RuneCountInString(d) != 1 || strings.ContainsAny(d, "\"\r\n") {
					return scheme.Value{}, fmt.Errorf("delimiter must be one character other than a quote or newline")
				}
				r.Comma = []rune(d)[0]
			}
			var rows []scheme.Value
			cells := 0
			for {
				rec, err := r.Read()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return scheme.Value{}, fmt.Errorf("bad CSV: %w", err)
				}
				if cells += len(rec); cells > maxCSVCells {
					return scheme.Value{}, fmt.Errorf("more than %d cells", maxCSVCells)
				}
				row := make([]scheme.Value, len(rec))
				for i, f := range rec {
					row[i] = scheme.Str(f)
				}
				rows = append(rows, scheme.List(row...))
			}
			return scheme.List(rows...), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "csv-emit",
		Meta: scheme.Meta{
			Doc: "CSV text for a list of rows, each a list of strings or numbers (quoted where needed, one line per row).",
			Params: []scheme.Param{
				{Name: "rows", Type: "list"},
				{Name: "delimiter", Type: "string", Optional: true},
			},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			rows, err := c.List(0)
			if err != nil {
				return scheme.Value{}, err
			}
			var sb strings.Builder
			w := csv.NewWriter(&sb)
			if len(c.Args) > 1 {
				d, err := c.String(1)
				if err != nil {
					return scheme.Value{}, err
				}
				if utf8.RuneCountInString(d) != 1 || strings.ContainsAny(d, "\"\r\n") {
					return scheme.Value{}, fmt.Errorf("delimiter must be one character other than a quote or newline")
				}
				w.Comma = []rune(d)[0]
			}
			for i, row := range rows {
				cells, ok := row.Slice()
				if !ok {
					return scheme.Value{}, fmt.Errorf("row %d is not a list", i+1)
				}
				rec := make([]string, len(cells))
				for j, cell := range cells {
					switch cell.Kind {
					case scheme.KString:
						rec[j] = cell.S
					case scheme.KInt, scheme.KFloat, scheme.KBool, scheme.KSymbol:
						rec[j] = scheme.Display(cell)
					default:
						return scheme.Value{}, fmt.Errorf("row %d, cell %d: expected a string or number, got %s", i+1, j+1, cell.Kind)
					}
				}
				if err := w.Write(rec); err != nil {
					return scheme.Value{}, err
				}
			}
			w.Flush()
			return scheme.Str(sb.String()), w.Error()
		},
	})
	s.add(&scheme.Prim{
		Name: "ini-parse",
		Meta: scheme.Meta{
			Doc: "Parse INI text into a list of (section entries...) pairs in order of first appearance: [section] headers, key=value lines, ; or # comment lines, and a valueless key whose value is \"\". Keys before any header are in the \"\" section. Whitespace around names and values is stripped and a duplicate key keeps its last value.",
			Params: text,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			in, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			var out []scheme.Value
			sections := map[string][]scheme.Value{}
			var order []string
			ensure := func(name string) {
				if _, ok := sections[name]; !ok {
					sections[name] = nil
					order = append(order, name)
				}
			}
			cur := ""
			for _, raw := range strings.Split(in, "\n") {
				line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
				if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
					continue
				}
				if strings.HasPrefix(line, "[") {
					if !strings.HasSuffix(line, "]") || len(line) < 3 {
						return scheme.Value{}, fmt.Errorf("bad INI section header: %q", raw)
					}
					cur = strings.TrimSpace(line[1 : len(line)-1])
					ensure(cur)
					continue
				}
				k, v, _ := strings.Cut(line, "=")
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				if k == "" {
					return scheme.Value{}, fmt.Errorf("bad INI line: %q", raw)
				}
				ensure(cur)
				ents := sections[cur]
				replaced := false
				for i, e := range ents {
					if e.P.Car.S == k {
						ents[i] = scheme.Cons(scheme.Str(k), scheme.Str(v))
						replaced = true
						break
					}
				}
				if !replaced {
					sections[cur] = append(ents, scheme.Cons(scheme.Str(k), scheme.Str(v)))
				}
			}
			for _, name := range order {
				out = append(out, scheme.Cons(scheme.Str(name), scheme.List(sections[name]...)))
			}
			return scheme.List(out...), nil
		},
	})
	return nil
}
