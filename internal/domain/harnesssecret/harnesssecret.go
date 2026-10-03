// Package harnesssecret defines a credential binding's OAuth token set for
// one org.
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
	// Scopes are the scopes the provider granted, as the token response's
	// space-delimited `scope` field reported them (RFC 6749 section 5.1).
	// The set is kept across a refresh that does not repeat the field.
	Scopes    []string
	UpdatedAt time.Time
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
