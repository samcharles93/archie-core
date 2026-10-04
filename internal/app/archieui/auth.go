package archieui

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/infrastructure/oidc"
)

// dashboardAuthenticator builds the OIDC credential check, or nil when no
// provider is configured.
func dashboardAuthenticator(ctx context.Context, opts Options, subjects identity.SubjectResolver, log *slog.Logger) (func(context.Context, string) (identity.Identity, error), error) {
	if strings.TrimSpace(opts.OidcIssuer) == "" {
		return nil, nil
	}
	verifier, err := oidc.New(ctx, oidc.Config{Issuer: opts.OidcIssuer, Audience: opts.OidcAudience})
	if err != nil {
		// Failing here rather than serving anyway: a dashboard that cannot verify
		// a credential would silently fall back to a gate that names nobody, and
		// an operator who configured a provider must not get the unauthenticated
		// behaviour instead.
		return nil, err
	}
	log.Info("dashboard authenticates provider credentials", "issuer", opts.OidcIssuer, "audience", opts.OidcAudience)
	return func(ctx context.Context, credential string) (identity.Identity, error) {
		// A personal token is bound in the State Store, not issued by the
		// provider, so it resolves without the provider's verifier.
		if subject, ok := identity.PersonalTokenSubject(credential); ok {
			return identity.Resolve(ctx, subjects, identity.Credential{Subject: subject})
		}
		value, _, err := identity.Authenticate(ctx, subjects, verifier, credential)
		return value, err
	}, nil
}

// dashboardLoginFlow builds the browser sign-in flow, or nil when not
// configured. The client secret is read from the named env var.
func dashboardLoginFlow(ctx context.Context, opts Options) (identity.LoginFlow, error) {
	if strings.TrimSpace(opts.OidcIssuer) == "" || strings.TrimSpace(opts.OidcClientID) == "" {
		return nil, nil
	}
	secret := ""
	if name := strings.TrimSpace(opts.OidcClientSecretEnv); name != "" {
		secret = os.Getenv(name)
	}
	return oidc.NewFlow(ctx, oidc.Config{Issuer: opts.OidcIssuer, Audience: opts.OidcAudience},
		opts.OidcClientID, secret, opts.OidcRedirectURL)
}
