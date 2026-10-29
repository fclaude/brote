package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

// TestStdDocCoverage mirrors scheme's TestDocCoverage: every procedure the
// library defines, in Go or in its Scheme prelude, appears in docs/std.md.
func TestStdDocCoverage(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "std.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	in := scheme.New()
	core := map[string]bool{}
	for _, p := range in.Prims() {
		core[p.Name] = true
	}
	rt, err := Install(in, Full())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	names := map[string]bool{}
	for _, p := range in.Prims() {
		if !core[p.Name] {
			names[p.Name] = true
			if p.Meta.Doc == "" {
				t.Errorf("%s has no doc string", p.Name)
			}
		}
	}
	define := regexp.MustCompile(`(?m)^\(define \(([^\s)]+)`)
	entries, _ := preludeFS.ReadDir("prelude")
	for _, e := range entries {
		src, err := preludeFS.ReadFile("prelude/" + e.Name())
		if err != nil {
			continue
		}
		for _, m := range define.FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
	}
	for name := range names {
		if !strings.Contains(text, "`("+name+" ") && !strings.Contains(text, "`("+name+")") && !strings.Contains(text, "`"+name+"`") {
			t.Errorf("%q is not documented in docs/std.md", name)
		}
	}
	if len(names) < 40 {
		t.Errorf("found only %d procedures: the test is not looking at the library", len(names))
	}
}
