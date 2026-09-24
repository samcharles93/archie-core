package bindingcipher

import (
	"encoding/base64"
	"strings"
	"testing"
)

// testBindingKey is high-entropy 32-byte-style material for cipher tests. It
// must be split across the fingerprint and key derivation consistently.
const testBindingKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestBindingCipherRoundTrip(t *testing.T) {
	c, err := NewBindingCipher(testBindingKey, nil)
	if err != nil {
		t.Fatalf("NewBindingCipher: %v", err)
	}
	plain := "sentry-hmac-secret-0123456789abcdef"
	env, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if env == plain {
		t.Fatal("Encrypt returned the plaintext")
	}
	if !strings.HasPrefix(env, "arcie-binding:v1:") {
		t.Fatalf("envelope prefix = %q, want arcie-binding:v1:", env)
	}
	got, err := c.Decrypt(env)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("round-trip = %q, want %q", got, plain)
	}
}

func TestBindingCipherWrongKeyFails(t *testing.T) {
	c, _ := NewBindingCipher(testBindingKey, nil)
	env, err := c.Encrypt("secret-0123456789abcdef")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	c2, _ := NewBindingCipher("a-completely-different-key-0000000000000000", nil)
	if _, err := c2.Decrypt(env); err == nil {
		t.Fatal("Decrypt with the wrong key succeeded, want error")
	}
}

func TestBindingCipherTamperedCiphertextFails(t *testing.T) {
	c, _ := NewBindingCipher(testBindingKey, nil)
	env, err := c.Encrypt("secret-0123456789abcdef")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := tamperEnvelope(t, env)
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("Decrypt of tampered envelope succeeded, want authentication error")
	}
}

func TestBindingCipherUnrecognisedEnvelopeFails(t *testing.T) {
	c, _ := NewBindingCipher(testBindingKey, nil)
	for _, bad := range []string{"", "not-an-envelope", "arcie-binding:v2:deadbeef:AAAA"} {
		if _, err := c.Decrypt(bad); err == nil {
			t.Errorf("Decrypt(%q) succeeded, want error", bad)
		}
	}
}

func TestBindingCipherKeyNotInKeyringFails(t *testing.T) {
	c, _ := NewBindingCipher("key-0-material-0000000000000000", nil)
	env, err := c.Encrypt("secret-0123456789abcdef")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// A keyring without the sealing key cannot decrypt the row.
	c2, _ := NewBindingCipher(testBindingKey, nil)
	if _, err := c2.Decrypt(env); err == nil {
		t.Fatal("Decrypt via a keyring missing the sealing key succeeded, want error")
	}
}

func TestBindingCipherRotationKeepsOldRowsReadable(t *testing.T) {
	oldKey := "old-key-material-000000000000000000000000"
	cOld, _ := NewBindingCipher(oldKey, nil)
	env, err := cOld.Encrypt("secret-0123456789abcdef")
	if err != nil {
		t.Fatalf("Encrypt (old): %v", err)
	}

	// Rotate: active becomes a new key, old is retained for reads.
	cNew, err := NewBindingCipher(testBindingKey, []string{oldKey})
	if err != nil {
		t.Fatalf("NewBindingCipher (rotated): %v", err)
	}
	got, err := cNew.Decrypt(env)
	if err != nil {
		t.Fatalf("Decrypt old row under rotated keyring: %v", err)
	}
	if got != "secret-0123456789abcdef" {
		t.Fatalf("rotated decrypt = %q", got)
	}

	// Once the old key is dropped, the old row is unreadable.
	cDropped, _ := NewBindingCipher(testBindingKey, nil)
	if _, err := cDropped.Decrypt(env); err == nil {
		t.Fatal("Decrypt with rotated-out keyring succeeded, want error")
	}
}

func TestBindingCipherDerivesDistinctKeys(t *testing.T) {
	a, _ := NewBindingCipher("material-A", nil)
	b, _ := NewBindingCipher("material-B", nil)
	if a.activeFingerprint == b.activeFingerprint {
		t.Fatal("distinct materials produced the same fingerprint")
	}
	if string(a.activeKey) == string(b.activeKey) {
		t.Fatal("distinct materials produced the same derived key")
	}
}

// tamperEnvelope flips one byte inside the ciphertext region so Decrypt must
// fail authentication rather than return altered plaintext.
func tamperEnvelope(t *testing.T, envelope string) string {
	t.Helper()
	parts := strings.SplitN(envelope, ":", 4)
	if len(parts) != 4 {
		t.Fatalf("unexpected envelope: %q", envelope)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	// Flip a byte in the ciphertext region (not the nonce).
	idx := 12
	if len(raw) <= idx {
		t.Fatalf("payload too short to tamper: %d bytes", len(raw))
	}
	raw[idx] ^= 0x01
	parts[3] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ":")
}
