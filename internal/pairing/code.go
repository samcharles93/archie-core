// Package pairing implements device-pairing codes for gateway
// authorization: a short, human-typeable code an operator hands to a
// new chat user, which the daemon verifies before granting access.
package pairing

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// Alphabet is the set of characters a pairing code is drawn from: digits
// 2-9 and uppercase A-Z excluding I and O. It deliberately excludes 0,
// O, 1, and I  --  characters humans commonly misread or mistype when
// copying a code from a screen.
const Alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// CodeLength is the number of characters in a generated pairing code.
const CodeLength = 8

// GenerateCode returns a random CodeLength-character code drawn
// uniformly from Alphabet using crypto/rand.
func GenerateCode() (string, error) {
	max := big.NewInt(int64(len(Alphabet)))
	code := make([]byte, CodeLength)
	for i := range code {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("pairing: generate code: %w", err)
		}
		code[i] = Alphabet[n.Int64()]
	}
	return string(code), nil
}
