package std

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand/v2"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Rand] = installRand }

const maxRandHex = 1 << 16

func installRand(s *installer) error {
	s.add(&scheme.Prim{
		Name: "rand-int",
		Meta: scheme.Meta{Doc: "A pseudo-random integer from 0 up to but not including n. Not reproducible and not for secrets (use rand-hex).", Params: []scheme.Param{{Name: "n", Type: "integer"}}, TaintOpaque: true},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			n, err := c.Int(0)
			if err != nil {
				return scheme.Value{}, err
			}
			if n <= 0 {
				return scheme.Value{}, fmt.Errorf("argument 1: expected a positive integer, got %d", n)
			}
			return scheme.Int(mrand.Int64N(n)), nil
		},
	})
	s.add(&scheme.Prim{
		Name:    "rand-float",
		Meta:    scheme.Meta{Doc: "A pseudo-random float from 0 up to but not including 1.", TaintOpaque: true},
		Handler: func(c *scheme.Call) (scheme.Value, error) { return scheme.Float(mrand.Float64()), nil },
	})
	s.add(&scheme.Prim{
		Name: "rand-shuffle",
		Meta: scheme.Meta{Doc: "A new list with the elements of a list in pseudo-random order.", Params: []scheme.Param{{Name: "list", Type: "list"}}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			items, err := c.List(0)
			if err != nil {
				return scheme.Value{}, err
			}
			mrand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
			return scheme.List(items...), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "rand-hex",
		Meta: scheme.Meta{Doc: "A string of 2n hexadecimal digits from the system's cryptographic random source, for tokens and identifiers. n is at most 65536.", Params: []scheme.Param{{Name: "n", Type: "integer", Doc: "number of random bytes"}}, TaintOpaque: true},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			n, err := c.Int(0)
			if err != nil {
				return scheme.Value{}, err
			}
			if n < 1 || n > maxRandHex {
				return scheme.Value{}, fmt.Errorf("argument 1: expected 1 to %d, got %d", maxRandHex, n)
			}
			b := make([]byte, n)
			if _, err := rand.Read(b); err != nil {
				return scheme.Value{}, err
			}
			return scheme.Str(hex.EncodeToString(b)), nil
		},
	})
	return nil
}
