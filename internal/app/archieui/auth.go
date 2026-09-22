package archieui

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/infrastructure/oidc"
)

// dashboardAuthenticator builds the dashboard's credential check from the
// endpoint references the process was given, or returns nil when none were.
//
// It is built here rather than in the command because cmd/ parses flags and
// nothing else, and it takes two URLs rather than a configuration struct because
// this process is deliberately kept unaware of the config file's shape -- see the
// note above Options. Issuer and audience are exactly the endpoint references
// Options already carries for the Gateway and the State Store.
//
// A nil result means no provider is configured, and the shared token stays the
// gate: an instance that never configured one keeps working, and its requests are
// recorded unattributed rather than credited to a human.
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
		value, _, err := identity.Authenticate(ctx, subjects, verifier, credential)
		return value, err
	}, nil
}

// dashboardLoginFlow builds the browser sign-in flow from endpoint references and
// the operator's registration, or returns nil when none is configured.
//
// The secret is read from the environment variable the operator named and never
// logged, printed or stored. A client id is optional: an instance that only
// accepts agent tokens configures none, and then there is simply no sign-in
// route -- token verification is unaffected.
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
