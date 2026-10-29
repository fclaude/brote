package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/brote/repl"
	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// The pages below hold examples that run. An example is a fenced block marked
// by the comment on the line before it:
//
//	<!-- run -->            a ```lisp block, fed to the REPL; the ```text block
//	                        after it, if there is one, is what the REPL printed
//	<!-- run-shell -->      a ```console block of "$ command" lines, each run
//	                        with the built brote on the PATH, in an empty
//	                        directory; the lines under a command are its output
//
// Every example on a page runs in the same interpreter, in order, so a page
// can build on what it defined earlier. DOCS_UPDATE=1 go test ./cmd/brote
// -run TestDocExamples rewrites the output blocks from what the examples do;
// without it the test fails when a block differs, so a page cannot drift from
// the program.
var docPages = []string{"README.md", "docs/tour.md", "docs/stdlib.md", "docs/command.md", "docs/scheme.md", "docs/embedding.md", "docs/capabilities.md", "docs/std.md"}

func TestDocExamples(t *testing.T) {
	update := os.Getenv("DOCS_UPDATE") != ""
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// The binary the shell examples run is built once, in a directory the
	// whole test owns, and only when a page has such an example.
	bin := ""
	for _, page := range docPages {
		if src, err := os.ReadFile(filepath.Join(root, page)); err == nil && bytes.Contains(src, []byte("<!-- run-shell -->")) {
			bin = buildBrote(t)
			break
		}
	}
	for _, page := range docPages {
		page := page
		t.Run(page, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(root, page))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			in := scheme.New(scheme.WithOutput(&out), scheme.WithTasks(scheme.TaskLimits{}))
			rt, err := std.Install(in, std.Full())
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			if err := registerHost(in, rt, nil, strings.NewReader(""), false); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(src), "\n")
			var result []string
			for i := 0; i < len(lines); i++ {
				result = append(result, lines[i])
				marker := strings.TrimSpace(lines[i])
				if marker != "<!-- run -->" && marker != "<!-- run-shell -->" {
					continue
				}
				// The block that follows.
				j := i + 1
				for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
					result = append(result, lines[j])
					j++
				}
				if j >= len(lines) || !strings.HasPrefix(lines[j], "```") {
					t.Fatalf("%s:%d: the marker is not followed by a fenced block", page, i+1)
				}
				open := j
				j++
				for j < len(lines) && !strings.HasPrefix(lines[j], "```") {
					j++
				}
				if j >= len(lines) {
					t.Fatalf("%s:%d: the block is never closed", page, open+1)
				}
				body := lines[open+1 : j]
				result = append(result, lines[open:j+1]...)
				i = j

				var got string
				if marker == "<!-- run -->" {
					out.Reset()
					repl.Run(context.Background(), in, strings.NewReader(strings.Join(body, "\n")+"\n"), &out, repl.Options{})
					got = strings.TrimRight(out.String(), "\n")
				} else {
					newBody, ok := runShellBlock(t, bin, body)
					if update {
						result = append(result[:len(result)-len(body)-1], append(newBody, "```")...)
					} else if !ok {
						t.Errorf("%s:%d: the commands print something else than the page shows:\n%s", page, open+1, strings.Join(newBody, "\n"))
					}
					continue
				}

				// The expected output block, if one follows.
				k := i + 1
				for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
					k++
				}
				hasOut := k < len(lines) && strings.TrimSpace(lines[k]) == "```text"
				var want string
				outEnd := k
				if hasOut {
					e := k + 1
					for e < len(lines) && !strings.HasPrefix(lines[e], "```") {
						e++
					}
					want = strings.Join(lines[k+1:e], "\n")
					outEnd = e
				}
				if update {
					if got != "" {
						result = append(result, "", "```text", got, "```")
					}
				} else if got != want {
					t.Errorf("%s:%d: the example prints\n%s\nbut the page shows\n%s", page, open+1, got, want)
				}
				if hasOut {
					i = outEnd
				}
			}
			if update {
				if err := os.WriteFile(filepath.Join(root, page), []byte(strings.Join(result, "\n")), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func buildBrote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "brote")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return dir
}

// runShellBlock runs the "$ command" lines of body and returns the block as
// the commands print it, and whether that is what body already said.
func runShellBlock(t *testing.T, binDir string, body []string) ([]string, bool) {
	t.Helper()
	work := t.TempDir()
	var cmds []string
	for _, l := range body {
		if strings.HasPrefix(l, "$ ") {
			cmds = append(cmds, l)
		}
	}
	var out []string
	for _, c := range cmds {
		out = append(out, c)
		cmd := exec.Command("sh", "-c", strings.TrimPrefix(c, "$ "))
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		b, _ := cmd.CombinedOutput()
		if s := strings.TrimRight(string(b), "\n"); s != "" {
			out = append(out, strings.Split(s, "\n")...)
		}
	}
	return out, strings.Join(out, "\n") == strings.Join(body, "\n")
}
