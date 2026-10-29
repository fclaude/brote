package std

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Regexp] = installRegexp }

const (
	maxPattern     = 4096
	maxCompiled    = 64
	maxRegexpFinds = 100_000
)

// regexps compiles patterns with Go's RE2 engine, which runs in time linear in
// the input whatever the pattern, so a hostile pattern cannot hang the host.
// It keeps the last few compiled.
type regexps struct {
	mu    sync.Mutex // scripts may match from several tasks at once
	cache map[string]*regexp.Regexp
}

func (r *regexps) get(pat string) (*regexp.Regexp, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if re, ok := r.cache[pat]; ok {
		return re, nil
	}
	if len(pat) > maxPattern {
		return nil, fmt.Errorf("pattern is longer than %d bytes", maxPattern)
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("bad pattern: %w", err)
	}
	if len(r.cache) >= maxCompiled {
		clear(r.cache)
	}
	r.cache[pat] = re
	return re, nil
}

// groups turns one match (index pairs into s) into a list: the whole match,
// then each group, #f for a group that did not take part.
func groups(s string, loc []int) scheme.Value {
	out := make([]scheme.Value, len(loc)/2)
	for i := range out {
		if loc[2*i] < 0 {
			out[i] = scheme.False
		} else {
			out[i] = scheme.Str(s[loc[2*i]:loc[2*i+1]])
		}
	}
	return scheme.List(out...)
}

func installRegexp(s *installer) error {
	rx := &regexps{cache: map[string]*regexp.Regexp{}}
	re2 := []scheme.Param{
		{Name: "pattern", Type: "string", Doc: "a Go (RE2) regular expression"},
		{Name: "text", Type: "string"},
	}
	// args compiles argument 0 and returns argument 1.
	args := func(c *scheme.Call) (*regexp.Regexp, string, error) {
		pat, err := c.String(0)
		if err != nil {
			return nil, "", err
		}
		text, err := c.String(1)
		if err != nil {
			return nil, "", err
		}
		re, err := rx.get(pat)
		return re, text, err
	}
	s.add(&scheme.Prim{
		Name: "regexp-matches?",
		Meta: scheme.Meta{Doc: "True when the pattern matches anywhere in the text. Anchor with ^ and $ to match all of it.", Params: re2, TaintOpaque: true},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			re, text, err := args(c)
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.Bool(re.MatchString(text)), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "regexp-match",
		Meta: scheme.Meta{Doc: "The first match of the pattern in the text, as a list of the whole match followed by each group (#f for a group that did not take part), or #f when there is none.", Params: re2},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			re, text, err := args(c)
			if err != nil {
				return scheme.Value{}, err
			}
			loc := re.FindStringSubmatchIndex(text)
			if loc == nil {
				return scheme.False, nil
			}
			return groups(text, loc), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "regexp-match-all",
		Meta: scheme.Meta{Doc: "Every non-overlapping match of the pattern in the text, each as regexp-match gives it. At most 100000 are returned.", Params: re2},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			re, text, err := args(c)
			if err != nil {
				return scheme.Value{}, err
			}
			all := re.FindAllStringSubmatchIndex(text, maxRegexpFinds)
			out := make([]scheme.Value, len(all))
			for i, loc := range all {
				out[i] = groups(text, loc)
			}
			return scheme.List(out...), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "regexp-replace",
		Meta: scheme.Meta{
			Doc: "The text with every match of the pattern replaced. In the replacement, $1 or ${name} stand for a group and $$ for a dollar sign.",
			Params: []scheme.Param{
				{Name: "pattern", Type: "string"}, {Name: "text", Type: "string"}, {Name: "replacement", Type: "string"},
			},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			re, text, err := args(c)
			if err != nil {
				return scheme.Value{}, err
			}
			repl, err := c.String(2)
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.Str(re.ReplaceAllString(text, repl)), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "regexp-split",
		Meta: scheme.Meta{Doc: "The pieces of the text between matches of the pattern, as a list of strings.", Params: re2},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			re, text, err := args(c)
			if err != nil {
				return scheme.Value{}, err
			}
			parts := re.Split(text, -1)
			out := make([]scheme.Value, len(parts))
			for i, p := range parts {
				out[i] = scheme.Str(p)
			}
			return scheme.List(out...), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "regexp-quote",
		Meta: scheme.Meta{Doc: "The text with every regular-expression metacharacter escaped, so a pattern made from it matches the text literally.", Params: []scheme.Param{{Name: "text", Type: "string"}}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			return str1(c, regexp.QuoteMeta)
		},
	})
	return nil
}
