package std

import (
	"fmt"
	"os"
	"slices"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Env] = installEnv }

// TagEnv marks a value read from the process environment.
const TagEnv = "external:env"

func installEnv(s *installer) error {
	scope := s.pol.Env
	allowed := func(name string) bool { return scope.Any || slices.Contains(scope.Allow, name) }
	s.add(&scheme.Prim{
		Name: "env-get",
		Meta: scheme.Meta{
			Doc: "The value of an environment variable, the default (#f when none is given) if it is unset. A variable the host did not grant is an error.",
			Params: []scheme.Param{
				{Name: "name", Type: "string"},
				{Name: "default", Type: "any", Optional: true},
			},
			Host:        true,
			TaintSource: TagEnv,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			name, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			if !allowed(name) {
				return scheme.Value{}, fmt.Errorf("environment variable %s is not granted", name)
			}
			if v, ok := os.LookupEnv(name); ok {
				return scheme.Str(v), nil
			}
			if len(c.Args) > 1 {
				return c.Args[1], nil
			}
			return scheme.False, nil
		},
	})
	s.add(&scheme.Prim{
		Name: "env-list",
		Meta: scheme.Meta{
			Doc:         "The granted environment variables that are set, as an association list of name and value strings.",
			Host:        true,
			TaintSource: TagEnv,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			var out []scheme.Value
			for _, kv := range os.Environ() {
				for i := 1; i < len(kv); i++ { // i from 1: a Windows =C: entry starts with =
					if kv[i] == '=' {
						if name := kv[:i]; allowed(name) {
							out = append(out, scheme.Cons(scheme.Str(name), scheme.Str(kv[i+1:])))
						}
						break
					}
				}
			}
			slices.SortFunc(out, func(a, b scheme.Value) int {
				switch {
				case a.P.Car.S < b.P.Car.S:
					return -1
				case a.P.Car.S > b.P.Car.S:
					return 1
				}
				return 0
			})
			return scheme.List(out...), nil
		},
	})
	return nil
}
