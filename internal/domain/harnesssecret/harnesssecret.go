// Package harnesssecret is the OAuth token set a credential binding's
// service holds for one org (docs/prds/external-agent-harness.md,
// Credentials). The setup terminal writes the first capture; the egress
// proxy overwrites it on every token refresh. It is never a Kit-declared
// value and never resolved through the config secret registry the way an
// API-key binding's SecretRef is -- it is state archie itself captures and
// mutates.
package harnesssecret

import (
	"errors"
	"time"
)

// Secret is one org's captured OAuth token set for a credential binding's
// service.
type Secret struct {
	Org          string
	Service      string
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresAt    time.Time
	UpdatedAt    time.Time
}

// Validate rejects a Secret with no identity or no access token: the setup
// terminal capture and the proxy's refresh write both produce a complete
// token set or nothing.
func (s Secret) Validate() error {
	if s.Org == "" {
		return errors.New("harnesssecret: org is required")
	}
	if s.Service == "" {
		return errors.New("harnesssecret: service is required")
	}
	if s.AccessToken == "" {
		return errors.New("harnesssecret: access token is required")
	}
	return nil
}
