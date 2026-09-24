package source

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// NewPath returns a fresh UUIDv7 path: time-ordered, unguessable, and never
// colliding with another source.
func NewPath() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("source: generate path: %w", err)
	}
	return id.String(), nil
}

// NewSecret returns a fresh 32-byte HMAC secret, hex encoded.
func NewSecret() (string, error) {
	var b [MinSecretLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("source: generate secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidatePath checks a custom path is URL-safe: RFC 3986 unreserved
// characters only, so it needs no escaping, and never a dot segment.
func ValidatePath(path string) error {
	if path == "" || len(path) > MaxPathLen || path == "." || path == ".." {
		return ErrInvalidPath
	}
	for i := range len(path) {
		if !unreserved(path[i]) {
			return ErrInvalidPath
		}
	}
	return nil
}

func unreserved(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return c == '-' || c == '.' || c == '_' || c == '~'
}
