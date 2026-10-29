package std

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[FS] = installFS }

const maxListEntries = 100_000

// rootOps is what the fs procedures need of a directory tree. *os.Root
// provides it with the operating system's containment; plainRoot provides it
// without, for a grant that names no directory at all.
type rootOps interface {
	Open(name string) (*os.File, error)
	OpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	Stat(name string) (os.FileInfo, error)
	MkdirAll(name string, perm os.FileMode) error
	Remove(name string) error
	Rename(oldname, newname string) error
	Close() error
}

// plainRoot is the file system below dir with no containment. It stands in for
// os.Root when the policy grants the whole file system, where there is nothing
// to contain and a symbolic link with an absolute target (/tmp on macOS) must
// work.
type plainRoot struct{ dir string }

func (r plainRoot) path(name string) string            { return filepath.Join(r.dir, name) }
func (r plainRoot) Open(name string) (*os.File, error) { return os.Open(r.path(name)) }
func (r plainRoot) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(r.path(name), flag, perm)
}
func (r plainRoot) Stat(name string) (os.FileInfo, error) { return os.Stat(r.path(name)) }
func (r plainRoot) MkdirAll(name string, perm os.FileMode) error {
	return os.MkdirAll(r.path(name), perm)
}
func (r plainRoot) Remove(name string) error { return os.Remove(r.path(name)) }
func (r plainRoot) Rename(oldname, newname string) error {
	return os.Rename(r.path(oldname), r.path(newname))
}
func (r plainRoot) Close() error { return nil }

// fsRoot is one granted directory tree. It is opened on first use, as an
// os.Root, which the operating system keeps every operation inside: a ".."
// or a symbolic link that leads out of the tree is refused.
type fsRoot struct {
	dir   string
	write bool
	plain bool // the whole file system, unscoped
	root  rootOps
}

func (r *fsRoot) open() (rootOps, error) {
	if r.root != nil {
		return r.root, nil
	}
	if r.plain {
		r.root = plainRoot{r.dir}
		return r.root, nil
	}
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return nil, fmt.Errorf("the granted directory %s cannot be opened: %w", r.dir, unwrapPath(err))
	}
	r.root = root
	return root, nil
}

// fsys resolves a script's path to a root and a name inside it.
type fsys struct {
	roots             []*fsRoot
	anyRead, anyWrite bool
	volumes           map[string]*fsRoot
	base              string
	maxFile           int64
	audit             func(Event)
}

func (f *fsys) Close() error {
	var errs []error
	for _, r := range append(f.roots, f.volumeRoots()...) {
		if r.root != nil {
			errs = append(errs, r.root.Close())
			r.root = nil
		}
	}
	return errors.Join(errs...)
}

func (f *fsys) volumeRoots() []*fsRoot {
	out := make([]*fsRoot, 0, len(f.volumes))
	for _, r := range f.volumes {
		out = append(out, r)
	}
	return out
}

// resolve finds the granted tree that holds path and the name of path in it.
// A write needs a tree granted for writing.
func (f *fsys) resolve(path string, write bool) (*fsRoot, string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, "", fmt.Errorf("%q is not a path", path)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(f.base, abs)
	}
	abs = filepath.Clean(abs)
	var best *fsRoot
	for _, r := range f.roots {
		if (!write || r.write) && pathCovers(r.dir, abs) && (best == nil || len(r.dir) > len(best.dir)) {
			best = r
		}
	}
	if best == nil && (f.anyWrite || (f.anyRead && !write)) {
		vol := filepath.VolumeName(abs)
		if f.volumes == nil {
			f.volumes = map[string]*fsRoot{}
		}
		if f.volumes[vol] == nil {
			f.volumes[vol] = &fsRoot{dir: vol + string(filepath.Separator), write: f.anyWrite, plain: true}
		}
		best = f.volumes[vol]
	}
	if best == nil {
		what := "read"
		if write {
			what = "write"
		}
		return nil, "", fmt.Errorf("%s is outside the directories this host lets scripts %s", path, what)
	}
	rel, err := filepath.Rel(best.dir, abs)
	if err != nil {
		return nil, "", err
	}
	return best, rel, nil
}

// unwrapPath strips the operating-system detail from a file error, leaving
// the reason ("no such file or directory", "path escapes from parent").
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// ReadFile reads a file the way fs-read-text does, for hosts that register a
// procedure of their own over the same grant.
func (r *Runtime) ReadFile(path string) (string, error) {
	if r.fs == nil {
		return "", errors.New("the fs module is not granted")
	}
	return r.fs.readText(path)
}

func (f *fsys) fail(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", path, unwrapPath(err))
}

func (f *fsys) readText(path string) (string, error) {
	r, name, err := f.resolve(path, false)
	if err != nil {
		return "", err
	}
	root, err := r.open()
	if err != nil {
		return "", err
	}
	file, err := root.Open(name)
	if err != nil {
		return "", f.fail(path, err)
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return "", f.fail(path, err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s: is a directory", path)
	}
	if st.Size() > f.maxFile {
		return "", fmt.Errorf("%s: %d bytes is more than the %d this host allows", path, st.Size(), f.maxFile)
	}
	b, err := io.ReadAll(io.LimitReader(file, f.maxFile+1))
	if err != nil {
		return "", f.fail(path, err)
	}
	if int64(len(b)) > f.maxFile {
		return "", fmt.Errorf("%s: more than the %d bytes this host allows", path, f.maxFile)
	}
	return strings.ToValidUTF8(string(b), "�"), nil
}

func (f *fsys) writeText(path, text string, appendTo bool) error {
	if int64(len(text)) > f.maxFile {
		return fmt.Errorf("%s: %d bytes is more than the %d this host allows", path, len(text), f.maxFile)
	}
	r, name, err := f.resolve(path, true)
	if err != nil {
		return err
	}
	root, err := r.open()
	if err != nil {
		return err
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendTo {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	file, err := root.OpenFile(name, flag, 0o644)
	if err != nil {
		return f.fail(path, err)
	}
	if _, err := file.WriteString(text); err != nil {
		file.Close()
		return f.fail(path, err)
	}
	return f.fail(path, file.Close())
}

func installFS(s *installer) error {
	scope := s.pol.FS
	f := &fsys{anyRead: scope.AnyRead || scope.AnyWrite, anyWrite: scope.AnyWrite, base: s.base, maxFile: scope.MaxFile, audit: s.audit}
	if f.base == "" {
		f.base, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(f.base); err == nil {
		f.base = abs
	}
	for _, d := range scope.Write {
		f.roots = append(f.roots, &fsRoot{dir: d, write: true})
	}
	for _, d := range scope.Read {
		if !slices.Contains(scope.Write, d) {
			f.roots = append(f.roots, &fsRoot{dir: d})
		}
	}
	s.closers = append(s.closers, f)
	s.fs = f

	pathP := scheme.Param{Name: "path", Type: "string", Role: "source"}
	writeP := scheme.Param{Name: "path", Type: "string", Role: "path"}
	read := scheme.Meta{Host: true, TaintSource: scheme.TagFile}
	mutate := func(doc string, params ...scheme.Param) scheme.Meta {
		return scheme.Meta{Doc: doc, Params: params, Host: true, Irreversible: true}
	}
	with := func(m scheme.Meta, doc string, params ...scheme.Param) scheme.Meta {
		m.Doc, m.Params = doc, params
		return m
	}
	str := func(c *scheme.Call, i int) (string, error) { return c.String(i) }

	s.add(&scheme.Prim{
		Name: "fs-read-text",
		Meta: with(read, "The contents of a file as a string. Bytes that are not valid UTF-8 become U+FFFD. A file larger than the host's limit is an error.", pathP),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			text, err := f.readText(p)
			return scheme.Str(text), err
		},
	})
	write := func(name, doc string, appendTo bool) {
		s.add(&scheme.Prim{
			Name: name,
			Meta: mutate(doc, writeP, scheme.Param{Name: "text", Type: "string"}),
			Handler: func(c *scheme.Call) (scheme.Value, error) {
				p, err := str(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				text, err := str(c, 1)
				if err != nil {
					return scheme.Value{}, err
				}
				err = f.writeText(p, text, appendTo)
				op := "write"
				if appendTo {
					op = "append"
				}
				s.report(FS, op, p, err)
				return scheme.Void, err
			},
		})
	}
	write("fs-write-text", "Write a string to a file, creating it or replacing what it held. The directory must exist and be granted for writing.", false)
	write("fs-append-text", "Add a string to the end of a file, creating it if needed.", true)

	s.add(&scheme.Prim{
		Name: "fs-list",
		Meta: with(read, "The names in a directory, sorted, as a list of strings (not the directory's own path, and not . or ..). Use fs-stat to tell files from directories.", pathP),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			r, name, err := f.resolve(p, false)
			if err != nil {
				return scheme.Value{}, err
			}
			root, err := r.open()
			if err != nil {
				return scheme.Value{}, err
			}
			d, err := root.Open(name)
			if err != nil {
				return scheme.Value{}, f.fail(p, err)
			}
			defer d.Close()
			ents, err := d.ReadDir(maxListEntries + 1)
			if err != nil && !errors.Is(err, io.EOF) {
				return scheme.Value{}, f.fail(p, err)
			}
			if len(ents) > maxListEntries {
				return scheme.Value{}, fmt.Errorf("%s: more than %d entries", p, maxListEntries)
			}
			names := make([]string, len(ents))
			for i, e := range ents {
				names[i] = e.Name()
			}
			slices.Sort(names)
			out := make([]scheme.Value, len(names))
			for i, n := range names {
				out[i] = scheme.Str(n)
			}
			return scheme.List(out...), nil
		},
	})
	stat := func(p string) (fs.FileInfo, error) {
		r, name, err := f.resolve(p, false)
		if err != nil {
			return nil, err
		}
		root, err := r.open()
		if err != nil {
			return nil, err
		}
		st, err := root.Stat(name)
		if err != nil {
			return nil, f.fail(p, err)
		}
		return st, nil
	}
	s.add(&scheme.Prim{
		Name: "fs-stat",
		Meta: with(read, "What a file or directory is, as an association list: \"size\" (bytes), \"dir\" (#t for a directory), \"mode\" (permission bits as text such as \"0644\") and \"mtime\" (seconds since 1970, a float). A missing file is an error; see fs-exists?.", pathP),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			st, err := stat(p)
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.List(
				scheme.Cons(scheme.Str("size"), scheme.Int(st.Size())),
				scheme.Cons(scheme.Str("dir"), scheme.Bool(st.IsDir())),
				scheme.Cons(scheme.Str("mode"), scheme.Str(fmt.Sprintf("%04o", st.Mode().Perm()))),
				scheme.Cons(scheme.Str("mtime"), seconds(st.ModTime())),
			), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "fs-exists?",
		Meta: with(read, "True when the path names a file or directory, #f when it does not. A path outside the granted directories is an error, not #f.", pathP),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			_, err = stat(p)
			switch {
			case err == nil:
				return scheme.True, nil
			case errors.Is(err, fs.ErrNotExist):
				return scheme.False, nil
			}
			return scheme.Value{}, err
		},
	})
	s.add(&scheme.Prim{
		Name: "fs-mkdir",
		Meta: with(scheme.Meta{Host: true}, "Make a directory and any missing parents inside the granted tree. A directory that exists is fine.", writeP),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			err = func() error {
				r, name, err := f.resolve(p, true)
				if err != nil {
					return err
				}
				root, err := r.open()
				if err != nil {
					return err
				}
				if name == "." {
					return nil
				}
				if err := root.MkdirAll(name, 0o755); err != nil {
					return f.fail(p, err)
				}
				return nil
			}()
			s.report(FS, "mkdir", p, err)
			return scheme.Void, err
		},
	})
	s.add(&scheme.Prim{
		Name: "fs-remove",
		Meta: mutate("Delete a file or an empty directory. A missing path is an error. There is no recursive delete.", writeP),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			err = func() error {
				r, name, err := f.resolve(p, true)
				if err != nil {
					return err
				}
				if name == "." {
					return fmt.Errorf("%s: a granted directory itself cannot be removed", p)
				}
				root, err := r.open()
				if err != nil {
					return err
				}
				return f.fail(p, root.Remove(name))
			}()
			s.report(FS, "remove", p, err)
			return scheme.Void, err
		},
	})
	s.add(&scheme.Prim{
		Name: "fs-rename",
		Meta: mutate("Move a file or directory to a new path inside the same granted tree, replacing a file already there.", writeP, scheme.Param{Name: "to", Type: "string", Role: "path"}),
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			from, err := str(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			to, err := str(c, 1)
			if err != nil {
				return scheme.Value{}, err
			}
			err = func() error {
				r1, n1, err := f.resolve(from, true)
				if err != nil {
					return err
				}
				r2, n2, err := f.resolve(to, true)
				if err != nil {
					return err
				}
				if r1 != r2 {
					return fmt.Errorf("%s and %s are in different granted directories: write a copy and remove the original", from, to)
				}
				if n1 == "." || n2 == "." {
					return fmt.Errorf("a granted directory itself cannot be renamed")
				}
				root, err := r1.open()
				if err != nil {
					return err
				}
				return f.fail(from, root.Rename(n1, n2))
			}()
			s.report(FS, "rename", from+" -> "+to, err)
			return scheme.Void, err
		},
	})
	return nil
}
