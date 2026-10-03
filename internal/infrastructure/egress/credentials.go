package egress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// Sentinel is the value a sandbox sees in place of a proxy-managed key, so
// it can tell a credential is wired without ever holding it.
const Sentinel = "archie-proxy-managed"

// CredentialKind is how a run carries a credential service, decided by the
// org's binding. An absent service is not carried.
type CredentialKind string

const (
	// CredentialAPIKey is an API key the proxy injects from the org secret
	// the binding names.
	CredentialAPIKey CredentialKind = "apikey"
	// CredentialOAuth is the org's captured OAuth token set, which the proxy
	// holds: the binding names no secret of its own.
	CredentialOAuth CredentialKind = "oauth"
)

// ErrUnbound reports that the run has no binding for a service. It is the
// only resolver error an optional credential may pass over; any other error
// stops the request.
var ErrUnbound = errors.New("egress: credential not bound")

// Resolver supplies a credential's real value for one run. An implementation
// returns only secrets the run's credential carries, so what a Kit declares
// is always narrowed by what the org granted the run's identity.
type Resolver interface {
	Resolve(ctx context.Context, run, service string) (string, error)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(ctx context.Context, run, service string) (string, error)

func (f ResolverFunc) Resolve(ctx context.Context, run, service string) (string, error) {
	return f(ctx, run, service)
}

// injection is one compiled credential@1 apiKey inject rule.
type injection struct {
	service  string
	required bool
	runtime  bool
	domain   pattern
	header   string
	format   string
	basic    bool
	username string
}

// compileInjections compiles the credential@1 apiKey inject rules this run
// can fire. A service the run carries as OAuth contributes none: its value is
// the org's token set, which the proxy holds and injects on its own paths, so
// an API-key rule for it would only put an empty credential on the wire.
func compileInjections(creds []spec.CredentialCapability, bound map[string]CredentialKind) []injection {
	var out []injection
	for _, c := range creds {
		if c.APIKey == nil || bound[c.Service] == CredentialOAuth {
			continue
		}
		for _, rule := range c.APIKey.Inject {
			format := rule.Format
			if format == "" {
				format = "%s"
			}
			out = append(out, injection{
				service:  c.Service,
				required: c.Required,
				runtime:  c.Phase == "runtime",
				domain:   compilePattern(rule.Domain),
				header:   rule.Header,
				format:   format,
				basic:    strings.EqualFold(rule.Scheme, "basic"),
				username: rule.Username,
			})
		}
	}
	return out
}

// inject presents the session's credentials on a request policy has already
// admitted. Only rules for this phase and this host apply, so a key is never
// sent anywhere its Kit did not name.
func (p *Proxy) inject(ctx context.Context, s *Session, r *http.Request, host string, port int) error {
	atRuntime := s.atRun.Load()
	for _, rule := range s.injections {
		if rule.runtime != atRuntime || !rule.domain.matches(normalizeHost(host), port) {
			continue
		}
		if p.resolver == nil {
			if rule.required {
				return fmt.Errorf("credential %q is required and no resolver is configured", rule.service)
			}
			continue
		}
		secret, err := p.resolver.Resolve(ctx, s.run, rule.service)
		switch {
		case errors.Is(err, ErrUnbound) && !rule.required:
			continue
		case errors.Is(err, ErrUnbound):
			return fmt.Errorf("credential %q is required and not bound for this run", rule.service)
		case err != nil:
			return fmt.Errorf("resolve credential %q: %w", rule.service, err)
		}
		if rule.basic {
			r.SetBasicAuth(rule.username, secret)
			continue
		}
		r.Header.Set(rule.header, fmt.Sprintf(rule.format, secret))
	}
	return nil
}

// SentinelEnv is the environment a sandbox gets for its Kit's API keys: the
// sentinel under each named key. An inject-only key has no presence at all.
func SentinelEnv(creds []spec.CredentialCapability) []string {
	var env []string
	for _, c := range creds {
		if c.APIKey != nil && c.APIKey.Name != "" {
			env = append(env, c.APIKey.Name+"="+Sentinel)
		}
	}
	return env
}
