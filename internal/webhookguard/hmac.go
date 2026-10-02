package webhookguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifyHMAC checks a SHA-256 HMAC signature against body using secret,
// accepting a signature with or without the "sha256=" prefix GitHub and
// similar senders use. The digest is hex, which is case-insensitive by
// convention and by sender taste, so an uppercase signature verifies rather
// than masquerading as a wrong secret. An empty signature is never valid, even
// against an empty secret -- the absence of a signature must never be mistaken
// for one that happens to match.
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
