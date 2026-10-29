package scheme

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Char returns argument i as a rune.
func (c *Call) Char(i int) (rune, error) {
	if v := c.arg(i); v.Kind == KChar {
		return rune(v.I), nil
	}
	return 0, c.typeErr(i, "character")
}

func (in *Interp) registerChars() {
	any1 := []Param{{Name: "x", Type: "any"}}
	ch1 := []Param{{Name: "c", Type: "any"}}

	in.mustRegisterOpaque("char?", "True for characters.", any1, false, func(c *Call) (Value, error) {
		return Bool(c.Args[0].Kind == KChar), nil
	})
	in.mustRegister("char->integer", "The code point of a character.", ch1, false, func(c *Call) (Value, error) {
		r, err := c.Char(0)
		if err != nil {
			return Value{}, err
		}
		return Int(int64(r)), nil
	})
	in.mustRegister("integer->char", "The character with this code point.", []Param{{Name: "n", Type: "integer"}}, false, func(c *Call) (Value, error) {
		n, err := c.Int(0)
		if err != nil {
			return Value{}, err
		}
		if n < 0 || n > unicode.MaxRune || !utf8.ValidRune(rune(n)) {
			return Value{}, errorf(c.Name, "%d is not a Unicode code point", n)
		}
		return Char(rune(n)), nil
	})
	mapChar := func(name, doc string, f func(rune) rune) {
		in.mustRegister(name, doc, ch1, false, func(c *Call) (Value, error) {
			r, err := c.Char(0)
			if err != nil {
				return Value{}, err
			}
			return Char(f(r)), nil
		})
	}
	mapChar("char-upcase", "The upper-case form of a character.", unicode.ToUpper)
	mapChar("char-downcase", "The lower-case form of a character.", unicode.ToLower)
	test := func(name, doc string, f func(rune) bool) {
		in.mustRegisterOpaque(name, doc, ch1, false, func(c *Call) (Value, error) {
			r, err := c.Char(0)
			if err != nil {
				return Value{}, err
			}
			return Bool(f(r)), nil
		})
	}
	test("char-alphabetic?", "True for a letter.", unicode.IsLetter)
	test("char-numeric?", "True for a decimal digit.", unicode.IsDigit)
	test("char-whitespace?", "True for a space, tab, newline or other white space.", unicode.IsSpace)
	test("char-upper-case?", "True for an upper-case letter.", unicode.IsUpper)
	test("char-lower-case?", "True for a lower-case letter.", unicode.IsLower)
	in.mustRegisterOpaque("digit-value", "The value 0 to 9 of a decimal digit character, or #f for any other.", ch1, false, func(c *Call) (Value, error) {
		r, err := c.Char(0)
		if err != nil {
			return Value{}, err
		}
		if r >= '0' && r <= '9' {
			return Int(int64(r - '0')), nil
		}
		if unicode.IsDigit(r) {
			// Another script's digits come in runs of ten from zero.
			n := int64(0)
			for z := r - 1; unicode.IsDigit(z) && n < 9; z-- {
				n++
			}
			return Int(n), nil
		}
		return False, nil
	})

	cmp := func(name, doc string, fold bool, ok func(a, b rune) bool) {
		in.mustRegisterOpaque(name, doc, []Param{{Name: "a", Type: "any"}, {Name: "b", Type: "any"}}, true, func(c *Call) (Value, error) {
			for i := 0; i < len(c.Args); i++ {
				if _, err := c.Char(i); err != nil {
					return Value{}, err
				}
			}
			for i := 0; i+1 < len(c.Args); i++ {
				a, b := rune(c.Args[i].I), rune(c.Args[i+1].I)
				if fold {
					a, b = unicode.ToLower(a), unicode.ToLower(b)
				}
				if !ok(a, b) {
					return False, nil
				}
			}
			return True, nil
		})
	}
	cmp("char=?", "True if the characters are all the same.", false, func(a, b rune) bool { return a == b })
	cmp("char<?", "True if the characters increase by code point.", false, func(a, b rune) bool { return a < b })
	cmp("char>?", "True if the characters decrease by code point.", false, func(a, b rune) bool { return a > b })
	cmp("char<=?", "True if the characters never decrease by code point.", false, func(a, b rune) bool { return a <= b })
	cmp("char>=?", "True if the characters never increase by code point.", false, func(a, b rune) bool { return a >= b })
	cmp("char-ci=?", "Like char=?, ignoring case.", true, func(a, b rune) bool { return a == b })
}

// stringRange is the start and end optional arguments at i and i+1 of a call
// that takes a string, as rune indexes into runes.
func (c *Call) stringRange(i int, n int) (start, end int, err error) {
	start, end = 0, n
	if len(c.Args) > i {
		s, err := c.Int(i)
		if err != nil {
			return 0, 0, err
		}
		start = int(s)
	}
	if len(c.Args) > i+1 {
		e, err := c.Int(i + 1)
		if err != nil {
			return 0, 0, err
		}
		end = int(e)
	}
	if start < 0 || end > n || start > end {
		return 0, 0, errorf(c.Name, "range %d..%d out of bounds for length %d", start, end, n)
	}
	return start, end, nil
}

func (in *Interp) registerCharStrings() {
	sOpt := []Param{{Name: "s", Type: "string"}, {Name: "start", Type: "integer", Optional: true}, {Name: "end", Type: "integer", Optional: true}}

	in.mustRegister("string-ref", "The character at a rune index of a string.", []Param{{Name: "s", Type: "string"}, {Name: "k", Type: "integer"}}, false, func(c *Call) (Value, error) {
		s, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		k, err := c.Int(1)
		if err != nil {
			return Value{}, err
		}
		if k >= 0 {
			idx := int64(0)
			for _, r := range s {
				if idx == k {
					return Char(r), nil
				}
				idx++
			}
		}
		return Value{}, errorf(c.Name, "index %d out of range for length %d", k, utf8.RuneCountInString(s))
	})
	in.mustRegister("string->list", "The characters of a string, or of the part from start to end, as a list.", sOpt, false, func(c *Call) (Value, error) {
		s, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		runes := []rune(s)
		start, end, err := c.stringRange(1, len(runes))
		if err != nil {
			return Value{}, err
		}
		out := make([]Value, 0, end-start)
		for _, r := range runes[start:end] {
			out = append(out, Char(r))
		}
		return List(out...), nil
	})
	in.mustRegister("list->string", "A string of the characters in a list.", []Param{{Name: "l", Type: "list"}}, false, func(c *Call) (Value, error) {
		items, err := c.List(0)
		if err != nil {
			return Value{}, err
		}
		var sb strings.Builder
		for i, v := range items {
			if v.Kind != KChar {
				return Value{}, errorf(c.Name, "element %d is not a character: %s", i, Write(v))
			}
			sb.WriteRune(rune(v.I))
		}
		return Str(sb.String()), nil
	})
	in.mustRegister("string", "A string of the characters given.", nil, true, func(c *Call) (Value, error) {
		var sb strings.Builder
		for i := range c.Args {
			r, err := c.Char(i)
			if err != nil {
				return Value{}, err
			}
			sb.WriteRune(r)
		}
		return Str(sb.String()), nil
	})
	in.mustRegister("make-string", "A string of k copies of a character (a space if none is given).", []Param{{Name: "k", Type: "integer"}, {Name: "fill", Type: "any", Optional: true}}, false, func(c *Call) (Value, error) {
		k, err := c.Int(0)
		if err != nil {
			return Value{}, err
		}
		fill := ' '
		if len(c.Args) > 1 {
			if fill, err = c.Char(1); err != nil {
				return Value{}, err
			}
		}
		if k < 0 || k > 1<<26 {
			return Value{}, errorf(c.Name, "length %d is out of range", k)
		}
		return Str(strings.Repeat(string(fill), int(k))), nil
	})
	in.mustRegister("string-copy", "The part of a string from start to end; the whole string if they are not given.", sOpt, false, func(c *Call) (Value, error) {
		s, err := c.String(0)
		if err != nil {
			return Value{}, err
		}
		runes := []rune(s)
		start, end, err := c.stringRange(1, len(runes))
		if err != nil {
			return Value{}, err
		}
		return Str(string(runes[start:end])), nil
	})
	in.mustRegister("string-for-each", "Call a procedure on each character of a string, in order.", []Param{{Name: "f", Type: "any"}, {Name: "s", Type: "string"}}, false, func(c *Call) (Value, error) {
		f, err := c.Proc(0)
		if err != nil {
			return Value{}, err
		}
		s, err := c.String(1)
		if err != nil {
			return Value{}, err
		}
		for _, r := range s {
			if _, err := c.In.Apply(c.Ctx, f, []Value{Char(r)}); err != nil {
				return Value{}, err
			}
		}
		return Void, nil
	})
	in.mustRegister("string-map", "A string of the characters a procedure returns for each character of a string.", []Param{{Name: "f", Type: "any"}, {Name: "s", Type: "string"}}, false, func(c *Call) (Value, error) {
		f, err := c.Proc(0)
		if err != nil {
			return Value{}, err
		}
		s, err := c.String(1)
		if err != nil {
			return Value{}, err
		}
		var sb strings.Builder
		for _, r := range s {
			v, err := c.In.Apply(c.Ctx, f, []Value{Char(r)})
			if err != nil {
				return Value{}, err
			}
			if v.Kind != KChar {
				return Value{}, errorf(c.Name, "the procedure returned %s, not a character", Write(v))
			}
			sb.WriteRune(rune(v.I))
		}
		return Str(sb.String()), nil
	})
	strCmp := func(name, doc string, ok func(a, b string) bool) {
		in.mustRegisterOpaque(name, doc, []Param{{Name: "a", Type: "string"}, {Name: "b", Type: "string"}}, true, func(c *Call) (Value, error) {
			for i := 0; i+1 < len(c.Args); i++ {
				a, err := c.String(i)
				if err != nil {
					return Value{}, err
				}
				b, err := c.String(i + 1)
				if err != nil {
					return Value{}, err
				}
				if !ok(a, b) {
					return False, nil
				}
			}
			return True, nil
		})
	}
	strCmp("string>?", "Lexicographic ordering chain, decreasing.", func(a, b string) bool { return a > b })
	strCmp("string<=?", "Lexicographic ordering chain, never increasing in reverse.", func(a, b string) bool { return a <= b })
	strCmp("string>=?", "Lexicographic ordering chain, never decreasing in reverse.", func(a, b string) bool { return a >= b })
	strCmp("string-ci=?", "String equality chain, ignoring case.", strings.EqualFold)
}
