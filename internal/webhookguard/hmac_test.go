package webhookguard_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/webhookguard"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyHMAC(t *testing.T) {
	const secret = "shared-secret"
	body := []byte(`{"issue":{"number":42}}`)
	validSig := sign(secret, body)

	tests := []struct {
		name      string
		body      []byte
		signature string
		secret    string
		want      bool
	}{
		{"valid signature", body, validSig, secret, true},
		{"valid signature with sha256= prefix", body, "sha256=" + validSig, secret, true},
		{"uppercase hex signature", body, strings.ToUpper(validSig), secret, true},
		{"uppercase hex signature with sha256= prefix", body, "sha256=" + strings.ToUpper(validSig), secret, true},
		{"wrong secret", body, validSig, "different-secret", false},
		{"uppercase hex signature for a different secret", body, strings.ToUpper(sign("different-secret", body)), secret, false},
		{"tampered body", []byte(`{"issue":{"number":99}}`), validSig, secret, false},
		{"empty signature", body, "", secret, false},
		{"empty signature against empty secret", body, "", "", false},
		{"sha256= with no digest", body, "sha256=", secret, false},
		{"garbage signature", body, "not-a-hex-signature", secret, false},
		{"odd-length hex", body, validSig[:len(validSig)-1], secret, false},
		{"truncated digest", body, validSig[:len(validSig)/2], secret, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := webhookguard.VerifyHMAC(test.body, test.signature, test.secret)
			if got != test.want {
				t.Errorf("VerifyHMAC() = %v, want %v", got, test.want)
			}
		})
	}
}
