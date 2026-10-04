package archieui

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/infrastructure/oidc"
)

// signInProvider returns the configured identity provider, or nil when none
// is. It does not contact the provider: an outage is a health issue, not a
// reason to stop serving.
func signInProvider(opts Options) (*oidc.Provider, error) {
	if strings.TrimSpace(opts.OidcIssuer) == "" {
		return nil, nil
	}
	return oidc.NewProvider(oidc.Config{Issuer: opts.OidcIssuer, Audience: opts.OidcAudience})
}

// dashboardAuthenticator builds the credential check, or nil when no provider
// is configured.
func dashboardAuthenticator(provider *oidc.Provider, opts Options, subjects identity.SubjectResolver, log *slog.Logger) func(context.Context, string) (identity.Identity, error) {
	if provider == nil {
		return nil
	}
	verifier := oidc.NewVerifier(provider)
	log.Info("dashboard authenticates provider credentials", "issuer", opts.OidcIssuer, "audience", opts.OidcAudience)
	return func(ctx context.Context, credential string) (identity.Identity, error) {
		// A personal token is bound in the State Store, not issued by the
		// provider, so it resolves without the provider's verifier.
		if subject, ok := identity.PersonalTokenSubject(credential); ok {
			return identity.Resolve(ctx, subjects, identity.Credential{Subject: subject})
		}
		value, _, err := identity.Authenticate(ctx, subjects, verifier, credential)
		return value, err
	}
}

// dashboardLoginFlow builds the browser sign-in flow, or nil when not
// configured. The client secret is read from the named env var.
func dashboardLoginFlow(provider *oidc.Provider, opts Options) (identity.LoginFlow, error) {
	if provider == nil || strings.TrimSpace(opts.OidcClientID) == "" {
		return nil, nil
	}
	secret := ""
	if name := strings.TrimSpace(opts.OidcClientSecretEnv); name != "" {
		secret = os.Getenv(name)
	}
	return oidc.NewFlow(provider, opts.OidcClientID, secret, opts.OidcRedirectURL)
}
