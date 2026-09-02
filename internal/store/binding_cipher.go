package store

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

// bindingSecretAAD is the additional-authenticated-data domain separator. It
// binds a ciphertext to the binding-secret payload context so it cannot be
// relocated to a different field/column and still authenticate. It is
// deliberately NOT row-bound: binding secrets are random HMAC keys and are
// never used as lookup keys, so cross-row relocation is not a meaningful
// attack here (see docs/prds/binding-secret-encryption.md).
var bindingSecretAAD = []byte("arcie-binding-secret")

// bindingEnvelopeMarker and bindingEnvelopeVersion identify a sealed binding
// secret and the format version. A future cipher or KDF change bumps the
// version so old rows stay readable while new writes use the new format.
const (
	bindingEnvelopeMarker  = "arcie-binding"
	bindingEnvelopeVersion = "v1"
	// bindingNonceLen is GCM's recommended 12-byte nonce.
	bindingNonceLen = 12
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
	// Encrypt returns the sealed envelope for a plaintext secret.
	Encrypt(plaintext string) (string, error)
	// Decrypt returns the plaintext secret for a sealed envelope.
	Decrypt(envelope string) (string, error)
}

// bindingCipher is the AES-256-GCM BindingCipher. Keys are derived as
// SHA-256(resolved material) -- 32 bytes of high-entropy input re-shaped to a
// 256-bit key, so no passphrase-stretching KDF (Argon2id) is required. The
// active key seals new writes; previous keys are retained for decryption only
// during rotation.
type bindingCipher struct {
	activeKey         []byte
	activeFingerprint string
	keys              map[string][]byte // fingerprint(hex[:16]) → 32-byte AES key
}

// NewBindingCipher builds a bindingCipher over the active key material and any
// previous (decrypt-only) material. Key identity is intrinsic to the material
// (the SHA-256 fingerprint), so rotation needs no manual key IDs. It is a pure
// constructor: it never reads the secret registry, which the composition root
// owns.
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

// Encrypt seals plaintext with the active key and returns the versioned
// envelope: "arcie-binding:v1:<fingerprint>:<base64url(nonce‖ct‖tag)>".
func (c *bindingCipher) Encrypt(plaintext string) (string, error) {
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
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), bindingSecretAAD)
	payload := make([]byte, 0, bindingNonceLen+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return fmt.Sprintf("%s:%s:%s:%s", bindingEnvelopeMarker, bindingEnvelopeVersion,
		c.activeFingerprint, base64.RawURLEncoding.EncodeToString(payload)), nil
}

// Decrypt parses the envelope, looks up the key by its embedded fingerprint,
// and returns the plaintext. A fingerprint not present in the keyring (an old
// key dropped from config) fails rather than returning garbage.
func (c *bindingCipher) Decrypt(envelope string) (string, error) {
	parts := strings.SplitN(envelope, ":", 4)
	if len(parts) != 4 || parts[0] != bindingEnvelopeMarker || parts[1] != bindingEnvelopeVersion {
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
	plaintext, err := gcm.Open(nil, nonce, sealed, bindingSecretAAD)
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
