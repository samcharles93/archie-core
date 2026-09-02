package store

import (
	"encoding/base64"
	"path/filepath"
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

func TestStoreBindingSecretEncryptedAtRest(t *testing.T) {
	s := openTestWithBindingCipher(t)
	b := testBinding("sentry")
	id, err := s.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}

	// The raw column must not be the plaintext secret.
	var stored string
	if err := s.db.QueryRowContext(t.Context(), `SELECT secret FROM bindings WHERE id=?`, id).Scan(&stored); err != nil {
		t.Fatalf("read raw secret: %v", err)
	}
	if stored == b.Secret {
		t.Fatal("bindings.secret stored as plaintext")
	}
	if !strings.HasPrefix(stored, "arcie-binding:v1:") {
		t.Fatalf("stored secret = %q, want arcie-binding:v1: envelope", stored)
	}

	// GetBinding returns the plaintext secret.
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding: %+v, %v", got, err)
	}
	if got.Secret != b.Secret {
		t.Fatalf("GetBinding.Secret = %q, want %q", got.Secret, b.Secret)
	}
}

func TestStoreListBindingsDecryptsSecrets(t *testing.T) {
	s := openTestWithBindingCipher(t)
	if _, err := s.InsertBinding(t.Context(), testBinding("sentry")); err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	got, err := s.ListBindings(t.Context())
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListBindings = %d rows, want 1", len(got))
	}
	if got[0].Secret != testBinding("sentry").Secret {
		t.Fatalf("ListBindings.Secret not decrypted")
	}
}

func TestStoreBindingCipherNoKeyIsPlaintext(t *testing.T) {
	s := openTest(t) // no cipher -> legacy plaintext
	b := testBinding("sentry")
	id, err := s.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding: %v", err)
	}
	got, err := s.GetBinding(t.Context(), id)
	if err != nil || got == nil {
		t.Fatalf("GetBinding: %+v, %v", got, err)
	}
	if got.Secret != b.Secret {
		t.Fatalf("plaintext store round-trip = %q, want %q", got.Secret, b.Secret)
	}
}

// TestStoreEncryptedReadLeavesLegacyPlaintextAlone pins that enabling a
// cipher does not break rows written before it was configured: a non-envelope
// secret must remain readable as plaintext (encryption is not retroactive).
func TestStoreEncryptedReadLeavesLegacyPlaintextAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	plain, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open (plaintext): %v", err)
	}
	b := testBinding("sentry")
	id, err := plain.InsertBinding(t.Context(), b)
	if err != nil {
		t.Fatalf("InsertBinding (plaintext): %v", err)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	c, _ := NewBindingCipher(testBindingKey, nil)
	encrypted, err := Open(t.Context(), path, WithBindingCipher(c))
	if err != nil {
		t.Fatalf("Open (cipher): %v", err)
	}
	t.Cleanup(func() { _ = encrypted.Close() })
	got, err := encrypted.GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding under cipher: %v", err)
	}
	if got == nil {
		t.Fatal("GetBinding returned nil")
	}
	if got.Secret != b.Secret {
		t.Fatalf("legacy plaintext secret = %q, want %q (must stay readable)", got.Secret, b.Secret)
	}
}

func openTestWithBindingCipher(t *testing.T) *Store {
	t.Helper()
	c, err := NewBindingCipher(testBindingKey, nil)
	if err != nil {
		t.Fatalf("NewBindingCipher: %v", err)
	}
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"), WithBindingCipher(c))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// tamperEnvelope decodes the payload, flips one byte inside the sealed
// ciphertext (after the 12-byte nonce), and re-encodes it, so the auth tag
// must fail. It returns the envelope with a corrupt ciphertext.
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
