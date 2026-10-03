// Package bindingcipher seals binding secrets at rest with AES-256-GCM under
// a rotatable keyring.
package bindingcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// bindingEnvelopeVersion identifies the format version. A future cipher or
// KDF change bumps it so old rows stay readable while new writes use the new
// format. The version is shared by every domain; only the marker and AAD
// differ between them.
const bindingEnvelopeVersion = "v1"

// bindingNonceLen is GCM's recommended 12-byte nonce.
const bindingNonceLen = 12

// Domain is an envelope's marker and additional-authenticated-data: the
// separator that stops a ciphertext sealed for one column being relocated to
// a different one and still authenticating.
// Domains share the same keyring; only the marker and AAD differ.
type Domain struct {
	marker string
	aad    []byte
}

var (
	// BindingDomain seals binding and source webhook secrets. Not row-bound.
	BindingDomain = Domain{marker: "arcie-binding", aad: []byte("arcie-binding-secret")}
	// HarnessSecretDomain seals harness OAuth token sets
	// -- its own
	// separator, so a row cannot be moved from oauth_secrets into a
	// bindings-secret column (or back) and still authenticate.
	HarnessSecretDomain = Domain{marker: "arcie-harness", aad: []byte("arcie-harness-secret")}
)

// bindingKeyFingerprintLen is the number of hex characters of the
// SHA-256(material) fingerprint embedded in the envelope (8 bytes, far more
// than enough to distinguish a handful of rotation keys).
const bindingKeyFingerprintLen = 16

// BindingCipher encrypts and decrypts binding secrets at rest. The store
// calls it transparently on write and read; a nil cipher leaves secrets as
// plaintext (legacy behaviour). Implementations must be safe for concurrent
// use.
type BindingCipher interface {
	// Encrypt returns the sealed envelope for a plaintext secret, under
	// BindingDomain.
	Encrypt(plaintext string) (string, error)
	// Decrypt returns the plaintext secret for a sealed envelope sealed
	// under BindingDomain.
	Decrypt(envelope string) (string, error)
	// EncryptDomain and DecryptDomain are Encrypt/Decrypt for a caller-named
	// domain, so a second column (e.g. harness OAuth secrets) can share this
	// cipher's keyring without sharing BindingDomain's AAD.
	EncryptDomain(d Domain, plaintext string) (string, error)
	DecryptDomain(d Domain, envelope string) (string, error)
}

// bindingCipher is AES-256-GCM with keys SHA-256(material). The active key
// seals; previous keys only decrypt.
type bindingCipher struct {
	activeKey         []byte
	activeFingerprint string
	keys              map[string][]byte // fingerprint(hex[:16]) → 32-byte AES key
}

// NewBindingCipher builds a cipher over the active and previous key
// material. Keys are identified by fingerprint.
func NewBindingCipher(active string, previous []string) (*bindingCipher, error) {
	if active == "" {
		return nil, errors.New("store: binding cipher requires a non-empty active key")
	}
	fp := bindingKeyFingerprint(active)
	c := &bindingCipher{
		activeKey:         deriveBindingKey(active),
		activeFingerprint: fp,
		keys:              make(map[string][]byte, 1+len(previous)),
	}
	c.keys[fp] = c.activeKey
	for _, material := range previous {
		if material == "" {
			continue
		}
		c.keys[bindingKeyFingerprint(material)] = deriveBindingKey(material)
	}
	return c, nil
}

// Encrypt seals plaintext under BindingDomain with the active key.
func (c *bindingCipher) Encrypt(plaintext string) (string, error) {
	return c.EncryptDomain(BindingDomain, plaintext)
}

// Decrypt opens an envelope sealed under BindingDomain.
func (c *bindingCipher) Decrypt(envelope string) (string, error) {
	return c.DecryptDomain(BindingDomain, envelope)
}

// EncryptDomain seals plaintext with the active key and returns the versioned
// envelope: "<marker>:v1:<fingerprint>:<base64url(nonce‖ct‖tag)>".
func (c *bindingCipher) EncryptDomain(d Domain, plaintext string) (string, error) {
	block, err := aes.NewCipher(c.activeKey)
	if err != nil {
		return "", fmt.Errorf("store: binding cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("store: binding cipher: %w", err)
	}
	nonce := make([]byte, bindingNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("store: binding cipher: nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), d.aad)
	payload := make([]byte, 0, bindingNonceLen+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return fmt.Sprintf("%s:%s:%s:%s", d.marker, bindingEnvelopeVersion,
		c.activeFingerprint, base64.RawURLEncoding.EncodeToString(payload)), nil
}

// DecryptDomain decrypts an envelope sealed under d. An unknown key or wrong
// domain is an error.
func (c *bindingCipher) DecryptDomain(d Domain, envelope string) (string, error) {
	parts := strings.SplitN(envelope, ":", 4)
	if len(parts) != 4 || parts[0] != d.marker || parts[1] != bindingEnvelopeVersion {
		return "", errors.New("store: binding cipher: unrecognised envelope")
	}
	key, ok := c.keys[parts[2]]
	if !ok {
		return "", errors.New("store: binding cipher: key not in keyring (rotated out?)")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", fmt.Errorf("store: binding cipher: decode payload: %w", err)
	}
	if len(raw) < bindingNonceLen {
		return "", errors.New("store: binding cipher: payload too short")
	}
	nonce, sealed := raw[:bindingNonceLen], raw[bindingNonceLen:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("store: binding cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("store: binding cipher: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, sealed, d.aad)
	if err != nil {
		return "", fmt.Errorf("store: binding cipher: authenticate: %w", err)
	}
	return string(plaintext), nil
}

// deriveBindingKey reduces 32-byte high-entropy material to a 256-bit AES key.
// SHA-256 is sufficient because the input is already high-entropy; it is not a
// passphrase-stretcher.
func deriveBindingKey(material string) []byte {
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

// bindingKeyFingerprint returns a short stable identifier for key material,
// embedded in the envelope so a rotated row can find the key that sealed it.
func bindingKeyFingerprint(material string) string {
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])[:bindingKeyFingerprintLen]
}
