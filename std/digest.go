package std

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Digest] = installDigest }

func installDigest(s *installer) error {
	text := []scheme.Param{{Name: "text", Type: "string"}}
	def := func(name, doc string, h func() hash.Hash) {
		s.add(&scheme.Prim{Name: name, Meta: scheme.Meta{Doc: doc, Params: text},
			Handler: func(c *scheme.Call) (scheme.Value, error) {
				in, err := c.String(0)
				if err != nil {
					return scheme.Value{}, err
				}
				d := h()
				d.Write([]byte(in))
				return scheme.Str(hex.EncodeToString(d.Sum(nil))), nil
			}})
	}
	def("sha256", "The SHA-256 digest of a string's UTF-8 bytes, as lower-case hex.", sha256.New)
	def("sha512", "The SHA-512 digest of a string's UTF-8 bytes, as lower-case hex.", sha512.New)
	def("sha1", "The SHA-1 digest of a string's UTF-8 bytes, as lower-case hex. Broken for security; for checksums and compatibility.", sha1.New)
	def("md5", "The MD5 digest of a string's UTF-8 bytes, as lower-case hex. Broken for security; for checksums and compatibility.", md5.New)
	s.add(&scheme.Prim{
		Name: "hmac-sha256",
		Meta: scheme.Meta{
			Doc:    "The HMAC-SHA-256 of a message under a key, as lower-case hex.",
			Params: []scheme.Param{{Name: "key", Type: "string", Role: "credential"}, {Name: "message", Type: "string"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			key, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			msg, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			m := hmac.New(sha256.New, []byte(key))
			m.Write([]byte(msg))
			return scheme.Str(hex.EncodeToString(m.Sum(nil))), nil
		},
	})
	return nil
}
