package webhookguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifyHMAC checks a hex SHA-256 HMAC of body, with or without a "sha256="
// prefix, case-insensitively. An empty signature never verifies.
func VerifyHMAC(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}
	// Decode both sides to raw bytes and compare those: hex.DecodeString
	// accepts either case, and comparing digests rather than their text
	// keeps the comparison constant-time.
	supplied, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(supplied, mac.Sum(nil))
}
