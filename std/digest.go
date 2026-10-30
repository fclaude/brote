package std

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"math/big"
	"slices"
	"strings"

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

	// Signing and verifying. The private key is the caller's own credential
	// (the gate sees it as one), so signing needs no grant; verifying is
	// authorization, so the public key must be one the policy granted with
	// (digest (keys …)) — an ungranted key is an error, not a #f.
	keyOK := func(hexKey string) bool {
		return s.pol.Digest.Any || slices.Contains(s.pol.Digest.Keys, strings.ToLower(hexKey))
	}
	hexArg := func(c *scheme.Call, i int, what string, sizes ...int) ([]byte, error) {
		s, err := c.String(i)
		if err != nil {
			return nil, err
		}
		b, err := hex.DecodeString(strings.ToLower(s))
		if err != nil {
			return nil, fmt.Errorf("argument %d: %s is not hex", i+1, what)
		}
		if len(sizes) > 0 && !slices.Contains(sizes, len(b)) {
			return nil, fmt.Errorf("argument %d: %s is %d bytes, want one of %v", i+1, what, len(b), sizes)
		}
		return b, nil
	}
	s.add(&scheme.Prim{
		Name: "ed25519-sign",
		Meta: scheme.Meta{
			Doc: "The Ed25519 signature of a message under a private key, as lower-case hex. The key is 64 bytes (a full private key) or 32 bytes (a seed), as hex.",
			Params: []scheme.Param{
				{Name: "private-key", Type: "string", Doc: "hex private key or seed", Role: "credential"},
				{Name: "message", Type: "string"},
			},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			kb, err := hexArg(c, 0, "private key", 32, 64)
			if err != nil {
				return scheme.Value{}, err
			}
			msg, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			priv := ed25519.PrivateKey(kb)
			if len(kb) == 32 {
				priv = ed25519.NewKeyFromSeed(kb)
			}
			return scheme.Str(hex.EncodeToString(ed25519.Sign(priv, []byte(msg)))), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "ed25519-verify",
		Meta: scheme.Meta{
			Doc: "Whether a message's Ed25519 signature verifies under a public key (32 bytes, hex). The key must be one the policy granted with (digest (keys …)); a signature that does not verify is #f, a key the policy does not grant is an error.",
			Params: []scheme.Param{
				{Name: "public-key", Type: "string", Doc: "hex public key, granted in the policy"},
				{Name: "message", Type: "string"},
				{Name: "signature", Type: "string", Doc: "hex signature"},
			},
			TaintOpaque: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			pubHex, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			if !keyOK(pubHex) {
				return scheme.Value{}, fmt.Errorf("digest: public key %s is not granted (granted keys: %s)", pubHex, s.pol.Digest.grantedText())
			}
			kb, err := hexArg(c, 0, "public key", 32)
			if err != nil {
				return scheme.Value{}, err
			}
			msg, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			sigHex, err := c.String(2)
			if err != nil {
				return scheme.Value{}, err
			}
			sig, err := hex.DecodeString(strings.ToLower(sigHex))
			if err != nil || len(sig) != 64 {
				return scheme.False, nil
			}
			return scheme.Bool(ed25519.Verify(ed25519.PublicKey(kb), []byte(msg), sig)), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "ecdsa-sign",
		Meta: scheme.Meta{
			Doc: "The ECDSA P-256 signature of a message's SHA-256 under a private key, as DER hex. The key is a 32-byte scalar, as hex.",
			Params: []scheme.Param{
				{Name: "private-key", Type: "string", Doc: "hex 32-byte scalar", Role: "credential"},
				{Name: "message", Type: "string"},
			},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			db, err := hexArg(c, 0, "private key", 32)
			if err != nil {
				return scheme.Value{}, err
			}
			msg, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			curve := elliptic.P256()
			priv := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve}, D: new(big.Int).SetBytes(db)}
			priv.X, priv.Y = curve.ScalarBaseMult(db)
			sum := sha256.Sum256([]byte(msg))
			sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
			if err != nil {
				return scheme.Value{}, err
			}
			return scheme.Str(hex.EncodeToString(sig)), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "ecdsa-verify",
		Meta: scheme.Meta{
			Doc: "Whether a message's ECDSA P-256 signature (DER hex) verifies under a public key (a compressed point, hex). The key must be one the policy granted with (digest (keys …)); a signature that does not verify is #f, a key the policy does not grant is an error.",
			Params: []scheme.Param{
				{Name: "public-key", Type: "string", Doc: "hex compressed point, granted in the policy"},
				{Name: "message", Type: "string"},
				{Name: "signature", Type: "string", Doc: "DER hex signature"},
			},
			TaintOpaque: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			pubHex, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			if !keyOK(pubHex) {
				return scheme.Value{}, fmt.Errorf("digest: public key %s is not granted (granted keys: %s)", pubHex, s.pol.Digest.grantedText())
			}
			kb, err := hexArg(c, 0, "public key", 33)
			if err != nil {
				return scheme.Value{}, err
			}
			x, y := elliptic.UnmarshalCompressed(elliptic.P256(), kb)
			if x == nil {
				return scheme.Value{}, fmt.Errorf("argument 1: public key is not a P-256 point")
			}
			msg, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			sigHex, err := c.String(2)
			if err != nil {
				return scheme.Value{}, err
			}
			sig, err := hex.DecodeString(strings.ToLower(sigHex))
			if err != nil {
				return scheme.False, nil
			}
			sum := sha256.Sum256([]byte(msg))
			return scheme.Bool(ecdsa.VerifyASN1(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, sum[:], sig)), nil
		},
	})
	return nil
}
