package std

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestSignVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)
	privHex := hex.EncodeToString(priv)
	pol := fmt.Sprintf(`(policy (digest (keys "%s")))`, pubHex)

	// Sign and verify round trip; a tampered message does not verify.
	expect(t, pol, map[string]string{
		fmt.Sprintf(`(ed25519-verify "%s" "hello" (ed25519-sign "%s" "hello"))`, pubHex, privHex): `#t`,
		fmt.Sprintf(`(ed25519-verify "%s" "hellp" (ed25519-sign "%s" "hello"))`, pubHex, privHex): `#f`,
		fmt.Sprintf(`(ed25519-verify "%s" "hello" "not-hex")`, pubHex):                            `#f`,
	})
	// A different public key does not verify the signature: grant both keys,
	// and verification under the wrong one is #f.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	both := fmt.Sprintf(`(policy (digest (keys "%s" "%s")))`, pubHex, hex.EncodeToString(otherPub))
	expect(t, both, map[string]string{
		fmt.Sprintf(`(ed25519-verify "%s" "hello" (ed25519-sign "%s" "hello"))`, hex.EncodeToString(otherPub), privHex): `#f`,
	})

	// The policy naming the key is what admits verify: without it, fail closed.
	expectErr(t, `(policy (digest))`, map[string]string{
		fmt.Sprintf(`(ed25519-verify "%s" "hello" "ab")`, pubHex): "is not granted",
	})
	// Signing needs no granted key: the private key is the caller's credential.
	expect(t, `(policy (digest))`, map[string]string{
		fmt.Sprintf(`(string? (ed25519-sign "%s" "hello"))`, privHex): `#t`,
	})

	// ECDSA P-256: the policy names the compressed public key.
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecPub := elliptic.MarshalCompressed(elliptic.P256(), ecPriv.X, ecPriv.Y)
	d := make([]byte, 32)
	ecPriv.D.FillBytes(d)
	ecPol := fmt.Sprintf(`(policy (digest (keys "%s")))`, hex.EncodeToString(ecPub))
	expect(t, ecPol, map[string]string{
		fmt.Sprintf(`(ecdsa-verify "%s" "hello" (ecdsa-sign "%s" "hello"))`, hex.EncodeToString(ecPub), hex.EncodeToString(d)): `#t`,
		fmt.Sprintf(`(ecdsa-verify "%s" "hellp" (ecdsa-sign "%s" "hello"))`, hex.EncodeToString(ecPub), hex.EncodeToString(d)): `#f`,
	})
	expectErr(t, `(policy (digest))`, map[string]string{
		fmt.Sprintf(`(ecdsa-verify "%s" "hello" "ab")`, hex.EncodeToString(ecPub)): "is not granted",
		// A scalar that is not a valid P-256 private key is refused.
		`(ecdsa-sign "0000000000000000000000000000000000000000000000000000000000000000" "hello")`: "not a P-256 scalar",
	})
	// The pure profile, which digest is part of, is deterministic: the same
	// key and message give the same signature (RFC 6979).
	expect(t, `(policy (profile pure))`, map[string]string{
		fmt.Sprintf(`(equal? (ecdsa-sign "%[1]s" "hello") (ecdsa-sign "%[1]s" "hello"))`, hex.EncodeToString(d)): `#t`,
	})
}
