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
	var claims struct {
		Scope string `json:"scope"`
	}
	// Claims are read after verification and are advisory: the identity comes
	// from the verified subject and issuer, never from a claim a caller could
	// have written into an unverified token.
	_ = token.Claims(&claims)
	return identity.Credential{
		Subject: identity.Subject{Issuer: token.Issuer, Subject: token.Subject},
		Scopes:  strings.Fields(claims.Scope),
		Expires: token.Expiry,
	}, nil
}
