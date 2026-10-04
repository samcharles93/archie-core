// Package oidc verifies identity-provider tokens. It is a resource server: it
// validates a token the provider issued and never issues one, holds no client
// secret, and mints no session.
package oidc

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

// Provider discovers the identity provider on first use and keeps the result.
// A provider that is down at startup does not stop the dashboard: discovery
// is retried on the next request, and Check reports the outage as health.
type Provider struct {
	issuer   string
	audience string

	mu         sync.Mutex
	discovered *gooidc.Provider
}

// NewProvider validates cfg without contacting the provider.
func NewProvider(cfg Config) (*Provider, error) {
	issuer := strings.TrimSpace(cfg.Issuer)
	audience := strings.TrimSpace(cfg.Audience)
	if issuer == "" {
		return nil, fmt.Errorf("oidc: issuer is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("oidc: audience is required")
	}
	return &Provider{issuer: issuer, audience: audience}, nil
}

// get returns the discovered provider, discovering it now if no earlier
// attempt succeeded.
func (p *Provider) get(ctx context.Context) (*gooidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovered != nil {
		return p.discovered, nil
	}
	discovered, err := gooidc.NewProvider(ctx, p.issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: discover %q: %w", identity.ErrProviderUnavailable, p.issuer, err)
	}
	p.discovered = discovered
	return discovered, nil
}

// Check fetches the provider's discovery document afresh, so an outage after
// startup shows as well as one before it.
func (p *Provider) Check(ctx context.Context) error {
	_, err := gooidc.NewProvider(ctx, p.issuer)
	return err
}

// Verifier validates tokens against the provider's published signing keys.
type Verifier struct {
	provider *Provider
}

// NewVerifier returns a verifier for provider's tokens.
func NewVerifier(provider *Provider) *Verifier {
	return &Verifier{provider: provider}
}

// Verify checks the token's signature, issuer, audience and expiry against the
// provider's keys, and returns what it proved. Every check is the library's:
// signature comparison, claim validation and key rotation are not archie's code.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (identity.Credential, error) {
	if v == nil || v.provider == nil {
		return identity.Credential{}, fmt.Errorf("oidc: no verifier is configured")
	}
	discovered, err := v.provider.get(ctx)
	if err != nil {
		return identity.Credential{}, err
	}
	verifier := discovered.Verifier(&gooidc.Config{ClientID: v.provider.audience})
	token, err := verifier.Verify(ctx, strings.TrimSpace(rawToken))
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

// subjectFor returns the token's subject: sub for a person, client_id for a
// machine. A token with neither is refused.
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
