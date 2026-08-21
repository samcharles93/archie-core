package webhookguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifyHMAC checks a SHA-256 HMAC signature against body using secret,
// accepting a signature with or without the "sha256=" prefix GitHub and
// similar senders use. An empty signature is never valid, even against an
// empty secret -- the absence of a signature must never be mistaken for one
// that happens to match.
func VerifyHMAC(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}
	sigHex := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sigHex), []byte(expected))
}
