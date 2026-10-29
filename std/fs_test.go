package std

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

// fsFixture lays out
//
//	root/data/a.txt        granted for reading
//	root/data/link         symlink to ../secret.txt (outside)
//	root/data/up           symlink to .. (outside)
//	root/out/              granted for writing
//	root/secret.txt        granted to nobody
type fsFixture struct {
	root, data, out string
	events          []Event
}

func newFixture(t *testing.T) *fsFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fsFixture{root: root, data: filepath.Join(root, "data"), out: filepath.Join(root, "out")}
	for _, d := range []string{f.data, f.out} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(f.data, "a.txt"), []byte("alpha\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "secret.txt"), []byte("s3cret"), 0o644))
	must(os.Symlink("../secret.txt", filepath.Join(f.data, "link")))
	must(os.Symlink("..", filepath.Join(f.data, "up")))
	must(os.Symlink(filepath.Join(root, "secret.txt"), filepath.Join(f.data, "abs")))
	return f
}

func (f *fsFixture) eval(t *testing.T, p *Policy, src string) (string, error) {
	t.Helper()
	in := scheme.New()
	rt, err := Install(in, p, WithBaseDir(f.root), WithAudit(func(e Event) { f.events = append(f.events, e) }))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	defer rt.Close()
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		return "", err
	}
	return scheme.Write(v), nil
}

func (f *fsFixture) policy() *Policy { return Allow(FSRead(f.data), FSWrite(f.out)) }

func TestFSReadsGrantedTrees(t *testing.T) {
	f := newFixture(t)
	for src, want := range map[string]string{
		`(fs-read-text "data/a.txt")`:                     `"alpha\n"`,
		fmt.Sprintf(`(fs-read-text %q)`, f.data+"/a.txt"): `"alpha\n"`,
		`(fs-list "data")`:                                `("a.txt" "abs" "link" "up")`,
		`(assoc-ref (fs-stat "data/a.txt") "size")`:       `6`,
		`(assoc-ref (fs-stat "data") "dir")`:              `#t`,
		`(fs-exists? "data/a.txt")`:                       `#t`,
		`(fs-exists? "data/none")`:                        `#f`,
		`(tainted? (fs-read-text "data/a.txt"))`:          `#t`,
		`(fs-list "out")`:                                 `()`,
	} {
		got, err := f.eval(t, f.policy(), src)
		if err != nil || got != want {
			t.Errorf("%s\n got  %s, %v\n want %s", src, got, err, want)
		}
	}
}

// Every way out of a granted tree must fail, whichever way the path is spelled.
func TestFSEscapesAreRefused(t *testing.T) {
	f := newFixture(t)
	for src, want := range map[string]string{
		`(fs-read-text "../secret.txt")`:          "outside",
		`(fs-read-text "data/../secret.txt")`:     "outside",
		`(fs-read-text "/etc/passwd")`:            "outside",
		`(fs-read-text "data/link")`:              "escapes",
		`(fs-read-text "data/abs")`:               "escapes",
		`(fs-read-text "data/up/secret.txt")`:     "escapes",
		`(fs-list "data/up")`:                     "escapes",
		`(fs-stat "data/link")`:                   "escapes",
		`(fs-exists? "/etc/passwd")`:              "outside",
		`(fs-exists? "data/link")`:                "escapes",
		`(fs-write-text "data/new.txt" "x")`:      "outside",
		`(fs-write-text "data/a.txt" "x")`:        "outside",
		`(fs-write-text "out/../data/a.txt" "x")`: "outside",
		`(fs-write-text "../secret.txt" "x")`:     "outside",
		`(fs-mkdir "data/sub")`:                   "outside",
		`(fs-remove "data/a.txt")`:                "outside",
		`(fs-rename "data/a.txt" "out/a.txt")`:    "outside",
		`(fs-rename "out/x" "data/x")`:            "outside",
		`(fs-read-text "")`:                       "not a path",
	} {
		_, err := f.eval(t, f.policy(), src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", src, err, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.data, "a.txt")); string(b) != "alpha\n" {
		t.Errorf("a refused write changed a.txt to %q", b)
	}
}

func TestFSSymlinkOutOfAWriteTree(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink(f.root, filepath.Join(f.out, "root")); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		`(fs-write-text "out/root/secret.txt" "pwned")`,
		`(fs-write-text "out/root/data/new" "pwned")`,
		`(fs-mkdir "out/root/newdir")`,
		`(fs-remove "out/root/secret.txt")`,
	} {
		if _, err := f.eval(t, f.policy(), src); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("%s: %v, want a path-escapes error", src, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.root, "secret.txt")); string(b) != "s3cret" {
		t.Errorf("secret.txt is now %q", b)
	}
}

func TestFSWrites(t *testing.T) {
	f := newFixture(t)
	for src, want := range map[string]string{
		`(begin (fs-write-text "out/x.txt" "one") (fs-append-text "out/x.txt" "two") (fs-read-text "out/x.txt"))`:            `"onetwo"`,
		`(begin (fs-write-text "out/x.txt" "new") (fs-read-text "out/x.txt"))`:                                               `"new"`,
		`(begin (fs-mkdir "out/a/b/c") (fs-mkdir "out/a/b/c") (assoc-ref (fs-stat "out/a/b") "dir"))`:                        `#t`,
		`(begin (fs-write-text "out/m" "m") (fs-rename "out/m" "out/n") (list (fs-exists? "out/m") (fs-read-text "out/n")))`: `(#f "m")`,
		`(begin (fs-write-text "out/gone" "") (fs-remove "out/gone") (fs-exists? "out/gone"))`:                               `#f`,
		`(begin (fs-mkdir "out/emptydir") (fs-remove "out/emptydir") (fs-exists? "out/emptydir"))`:                           `#f`,
	} {
		got, err := f.eval(t, f.policy(), src)
		if err != nil || got != want {
			t.Errorf("%s\n got  %s, %v\n want %s", src, got, err, want)
		}
	}
	for src, want := range map[string]string{
		`(fs-remove "out")`:                 "granted directory itself",
		`(fs-remove "out/missing")`:         "no such file",
		`(fs-remove "out/a")`:               "", // not empty: any error
		`(fs-write-text "out/nodir/x" "x")`: "no such file",
		`(fs-read-text "out/a")`:            "is a directory",
		`(fs-rename "out" "out/z")`:         "granted directory itself",
	} {
		if _, err := f.eval(t, f.policy(), src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", src, err, want)
		}
	}
}

func TestFSAuditSeesMutations(t *testing.T) {
	f := newFixture(t)
	_, _ = f.eval(t, f.policy(), `(begin (fs-write-text "out/x" "1") (fs-mkdir "out/d") (fs-remove "out/x"))`)
	_, _ = f.eval(t, f.policy(), `(fs-write-text "data/x" "1")`)
	var got []string
	for _, e := range f.events {
		got = append(got, fmt.Sprintf("%s:%s:%v", e.Op, filepath.Base(e.Target), e.Err != nil))
	}
	if want := "write:x:false mkdir:d:false remove:x:false write:x:true"; strings.Join(got, " ") != want {
		t.Errorf("events %v, want %s", got, want)
	}
}

func TestFSLimits(t *testing.T) {
	f := newFixture(t)
	p := f.policy()
	p.FS.MaxFile = 10
	if err := os.WriteFile(filepath.Join(f.data, "big"), []byte(strings.Repeat("x", 11)), 0o644); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		`(fs-read-text "data/big")`:                "more than the 10",
		`(fs-write-text "out/big" "01234567890")`:  "more than the 10",
		`(fs-append-text "out/big" "01234567890")`: "more than the 10",
	} {
		if _, err := f.eval(t, p, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", src, err, want)
		}
	}
	if got, err := f.eval(t, p, `(fs-read-text "data/a.txt")`); err != nil || got != `"alpha\n"` {
		t.Errorf("a file within the limit: %s, %v", got, err)
	}
}

func TestFSInvalidUTF8BecomesReplacement(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.data, "bin"), []byte("a\xffb"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := f.eval(t, f.policy(), `(fs-read-text "data/bin")`)
	if err != nil || got != "\"a�b\"" {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestFSTwoWriteTreesCannotRenameAcross(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.root, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Allow(FSWrite(f.out, other))
	if _, err := f.eval(t, p, `(begin (fs-write-text "out/a" "1") (fs-rename "out/a" "other/a"))`); err == nil || !strings.Contains(err.Error(), "different granted") {
		t.Errorf("rename across trees: %v", err)
	}
}

func TestFSNestedGrants(t *testing.T) {
	f := newFixture(t)
	// A read grant on the whole root and a write grant on out/: writes outside out/ still fail.
	p := Allow(FSRead(f.root), FSWrite(f.out))
	if got, err := f.eval(t, p, `(fs-read-text "secret.txt")`); err != nil || got != `"s3cret"` {
		t.Errorf("read of the wider grant: %s, %v", got, err)
	}
	if _, err := f.eval(t, p, `(fs-write-text "secret.txt" "x")`); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("write outside the write tree: %v", err)
	}
	if _, err := f.eval(t, p, `(fs-write-text "out/ok" "x")`); err != nil {
		t.Errorf("write inside: %v", err)
	}
}

func TestFSMissingGrantedDirIsAUseTimeError(t *testing.T) {
	f := newFixture(t)
	p := Allow(FSWrite(filepath.Join(f.root, "nope")))
	if _, err := f.eval(t, p, `(fs-write-text "nope/x" "x")`); err == nil || !strings.Contains(err.Error(), "cannot be opened") {
		t.Errorf("got %v", err)
	}
}

func TestFSUnscoped(t *testing.T) {
	f := newFixture(t)
	p := Full()
	got, err := f.eval(t, p, fmt.Sprintf(`(begin (fs-write-text %q "z") (fs-read-text %q))`, f.root+"/free.txt", f.root+"/free.txt"))
	if err != nil || got != `"z"` {
		t.Errorf("full: %s, %v", got, err)
	}
	// Symlinks that stay on the machine are followed under an unscoped grant, as they would be by any program.
	if got, err := f.eval(t, p, `(fs-read-text "data/link")`); err != nil || got != `"s3cret"` {
		t.Errorf("full follows symlinks: %s, %v", got, err)
	}
	if got, err := f.eval(t, p, `(fs-read-text "data/abs")`); err != nil || got != `"s3cret"` {
		t.Errorf("full follows absolute symlinks: %s, %v", got, err)
	}
	readOnly := Allow(Profile("full"))
	readOnly.FS = FSScope{AnyRead: true}
	readOnly.normalize()
	if _, err := f.eval(t, readOnly, `(fs-write-text "out/no" "x")`); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("read-everything must not write: %v", err)
	}
}

func TestRuntimeReadFile(t *testing.T) {
	f := newFixture(t)
	in := scheme.New()
	rt, err := Install(in, f.policy(), WithBaseDir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if s, err := rt.ReadFile("data/a.txt"); err != nil || s != "alpha\n" {
		t.Errorf("ReadFile = %q, %v", s, err)
	}
	if _, err := rt.ReadFile("secret.txt"); err == nil {
		t.Error("ReadFile reached outside the grant")
	}
}
