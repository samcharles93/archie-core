package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
)

// saltLen is the size in bytes of a generated salt. 16 bytes (128 bits)
// is standard practice for a salt  --  large enough that two salts
// colliding is negligible, without adding meaningful storage cost.
const saltLen = 16

// HashedCode is a salted SHA-256 digest of a pairing code, safe to store
// (the plaintext code itself never needs to be persisted after hashing).
type HashedCode struct {
	Salt []byte
	Hash []byte
}

// HashCode generates a random salt and returns the salted SHA-256 digest
// of code. Each call produces a different salt (and therefore a
// different Hash) even for the same code.
func HashCode(code string) (HashedCode, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return HashedCode{}, fmt.Errorf("pairing: generate salt: %w", err)
	}
	return HashedCode{Salt: salt, Hash: digest(salt, code)}, nil
}

// Verify reports whether code hashes to the same digest as h, using a
// constant-time comparison so the check doesn't leak timing information
// about how much of the digest matched.
func (h HashedCode) Verify(code string) bool {
	if len(h.Salt) == 0 || len(h.Hash) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(h.Hash, digest(h.Salt, code)) == 1
}

// digest computes SHA-256(salt || code).
func digest(salt []byte, code string) []byte {
	sum := sha256.New()
	sum.Write(salt)
	sum.Write([]byte(code))
	return sum.Sum(nil)
}
