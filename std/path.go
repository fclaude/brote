package std

import (
	"path/filepath"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Path] = installPath }

func str1(c *scheme.Call, f func(string) string) (scheme.Value, error) {
	s, err := c.String(0)
	if err != nil {
		return scheme.Value{}, err
	}
	return scheme.Str(f(s)), nil
}

func installPath(s *installer) error {
	one := []scheme.Param{{Name: "path", Type: "string"}}
	def := func(name, doc string, f func(string) string) {
		s.add(&scheme.Prim{Name: name, Meta: scheme.Meta{Doc: doc, Params: one},
			Handler: func(c *scheme.Call) (scheme.Value, error) { return str1(c, f) }})
	}
	def("path-base", "The last element of a path (\"a/b.txt\" gives \"b.txt\").", filepath.Base)
	def("path-dir", "A path without its last element (\"a/b.txt\" gives \"a\").", filepath.Dir)
	def("path-ext", "The extension of a path, with its dot (\"a/b.txt\" gives \".txt\"), or \"\".", filepath.Ext)
	def("path-clean", "A path with . and .. resolved and repeated separators removed, without touching the file system.", filepath.Clean)
	s.add(&scheme.Prim{
		Name: "path-join",
		Meta: scheme.Meta{
			Doc:      "Join path elements with the platform separator and clean the result. Empty elements are skipped.",
			Params:   []scheme.Param{{Name: "element", Type: "string"}},
			Variadic: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			parts := make([]string, len(c.Args))
			for i := range c.Args {
				p, err := c.String(i)
				if err != nil {
					return scheme.Value{}, err
				}
				parts[i] = p
			}
			return scheme.Str(filepath.Join(parts...)), nil
		},
	})
	return nil
}
