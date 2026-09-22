// Package oidc verifies identity-provider tokens. It is a resource server: it
// validates a token the provider issued and never issues one, holds no client
// secret, and mints no session.
package oidc

import (
	"context"
	"fmt"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// Config identifies the provider and the audience archie accepts tokens for.
// Audience is required rather than optional: without it a token the provider
// minted for a different service would authenticate here, which is the
// confused-deputy failure this boundary exists to prevent.
type Config struct {
	Issuer   string
	Audience string
}

// Verifier validates tokens against the provider's published signing keys.
type Verifier struct {
	verifier *gooidc.IDTokenVerifier
}

// New discovers the provider and builds a verifier from its published keys.
// Discovery happens once, at construction: an unreachable provider is a
// startup failure the operator sees, not a request-time surprise.
func New(ctx context.Context, cfg Config) (*Verifier, error) {
	issuer := strings.TrimSpace(cfg.Issuer)
	audience := strings.TrimSpace(cfg.Audience)
	if issuer == "" {
		return nil, fmt.Errorf("oidc: issuer is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("oidc: audience is required")
	}
	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %q: %w", issuer, err)
	}
	return &Verifier{verifier: provider.Verifier(&gooidc.Config{ClientID: audience})}, nil
}

// Verify checks the token's signature, issuer, audience and expiry against the
// provider's keys, and returns what it proved. Every check is the library's:
// signature comparison, claim validation and key rotation are not archie's code.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (identity.Credential, error) {
	if v == nil || v.verifier == nil {
		return identity.Credential{}, fmt.Errorf("oidc: no verifier is configured")
	}
	token, err := v.verifier.Verify(ctx, strings.TrimSpace(rawToken))
	if err != nil {
		return identity.Credential{}, fmt.Errorf("oidc: verify: %w", err)
	}
	// Claims are read after verification and are the only source of the
	// identity: an unverified token selects nothing.
	var claims struct {
		Subject  string   `json:"sub"`
		ClientID string   `json:"client_id"`
		Scope    string   `json:"scope"`
		Scopes   []string `json:"scp"`
	}
	_ = token.Claims(&claims)
	subject, err := subjectFor(token.Issuer, claims.Subject, claims.ClientID)
	if err != nil {
		return identity.Credential{}, err
	}
	return identity.Credential{
		Subject: subject,
		Scopes:  scopesFor(claims.Scope, claims.Scopes),
		Expires: token.Expiry,
	}, nil
}

// subjectFor derives the caller a token asserts.
//
// A person's token carries sub, the provider's stable identifier for that person.
// A machine's token carries no sub at all and identifies its caller by client_id,
// so a machine identity is keyed on the client and one identity exists per client
// registration. A token asserting neither identifies nobody and is refused rather
// than being accepted as an anonymous caller.
func subjectFor(issuer, subject, clientID string) (identity.Subject, error) {
	switch {
	case strings.TrimSpace(subject) != "":
		return identity.Subject{Issuer: issuer, Subject: strings.TrimSpace(subject)}, nil
	case strings.TrimSpace(clientID) != "":
		return identity.Subject{Issuer: issuer, Subject: strings.TrimSpace(clientID)}, nil
	default:
		return identity.Subject{}, fmt.Errorf("oidc: token asserts neither sub nor client_id")
	}
}

// scopesFor reads the granted scopes from either claim shape. Providers differ:
// the standard is a space-delimited scope string, and this provider also emits an
// scp array. Reading only one shape silently yields no scopes at all, which is
// worse than reading none deliberately.
func scopesFor(scope string, scopes []string) []string {
	if len(scopes) > 0 {
		return scopes
	}
	return strings.Fields(scope)
}
