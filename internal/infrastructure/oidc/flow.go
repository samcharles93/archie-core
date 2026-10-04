package oidc

import (
	"context"
	"fmt"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// Flow runs the authorization code flow with PKCE, so a person signs in at the
// provider and the dashboard learns who they are without archie ever seeing a
// password, holding a session, or writing any of the protocol itself: discovery,
// the code exchange, PKCE and token verification are the libraries'.
type Flow struct {
	provider     *Provider
	verifier     *Verifier
	clientID     string
	clientSecret string
	redirectURL  string
}

// NewFlow builds the browser flow over provider. ClientID and the secret are
// the provider's registration for this dashboard; the secret is read from
// wherever the operator keeps it and is never logged or stored.
func NewFlow(provider *Provider, clientID, clientSecret, redirectURL string) (*Flow, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("oidc: client id is required for the browser flow")
	}
	if strings.TrimSpace(redirectURL) == "" {
		return nil, fmt.Errorf("oidc: redirect URL is required for the browser flow")
	}
	return &Flow{
		provider: provider, verifier: NewVerifier(provider),
		clientID: clientID, clientSecret: clientSecret, redirectURL: redirectURL,
	}, nil
}

func (f *Flow) oauth(ctx context.Context) (oauth2.Config, error) {
	discovered, err := f.provider.get(ctx)
	if err != nil {
		return oauth2.Config{}, err
	}
	return oauth2.Config{
		ClientID:     f.clientID,
		ClientSecret: f.clientSecret,
		Endpoint:     discovered.Endpoint(),
		RedirectURL:  f.redirectURL,
		// openid is what makes the provider return a subject at all; profile
		// and email are what make a name available for a display.
		Scopes: []string{gooidc.ScopeOpenID, "profile", "email"},
	}, nil
}

// AuthCodeURL returns the provider URL to send a browser to, with the
// audience requested, and the PKCE verifier.
func (f *Flow) AuthCodeURL(ctx context.Context, state string) (string, string, error) {
	config, err := f.oauth(ctx)
	if err != nil {
		return "", "", err
	}
	codeVerifier := oauth2.GenerateVerifier()
	url := config.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeVerifier),
		oauth2.SetAuthURLParam("audience", f.provider.audience),
	)
	return url, codeVerifier, nil
}

// Exchange completes the flow: it trades the code for the provider's tokens and
// verifies the access token through the same Verifier a presented token goes
// through. There is no second verification path here, so a token obtained by the
// browser cannot be accepted on terms a presented token would be refused on.
func (f *Flow) Exchange(ctx context.Context, code, codeVerifier string) (identity.ProviderSession, error) {
	config, err := f.oauth(ctx)
	if err != nil {
		return identity.ProviderSession{}, err
	}
	token, err := config.Exchange(ctx, strings.TrimSpace(code), oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return identity.ProviderSession{}, fmt.Errorf("oidc: exchange code: %w", err)
	}
	credential, err := f.verifier.Verify(ctx, token.AccessToken)
	if err != nil {
		return identity.ProviderSession{}, err
	}
	expires := token.Expiry
	if credential.Expires.Before(expires) || expires.IsZero() {
		expires = credential.Expires
	}
	return identity.ProviderSession{Credential: credential, Token: token.AccessToken, Expires: expires}, nil
}
