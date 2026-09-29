package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
)

// maxTokenExchange bounds a token request or response body the proxy
// buffers to rewrite; real ones are a few hundred bytes.
const maxTokenExchange = 1 << 20

// OAuthStore persists one org's OAuth token set per credential binding
// service (docs/prds/external-agent-harness.md, Credentials). It is
// storecontract.HarnessSecretStore's method set, declared narrow here.
type OAuthStore interface {
	GetHarnessSecret(ctx context.Context, org, service string) (harnesssecret.Secret, error)
	PutHarnessSecret(ctx context.Context, s harnesssecret.Secret) error
}

// oauthRule is one compiled credential@1 OAuth credential: the token
// endpoint the proxy intercepts, the resource hosts whose requests carry the
// access token, the in-container sentinels and the provider's response
// field names.
type oauthRule struct {
	service        string
	required       bool
	runtime        bool
	tokenHost      pattern
	tokenPath      string
	resourceHosts  []pattern
	sentinels      spec.Sentinels
	responseFields spec.ResponseFields
}

// IsOAuthManaged reports whether the proxy holds c's tokens: an OAuth
// credential with a token endpoint that is not a passthrough downgrade.
func IsOAuthManaged(c spec.CredentialCapability) bool {
	return c.OAuth != nil && !c.OAuth.Passthrough && c.OAuth.TokenEndpoint != nil
}

func compileOAuthRules(creds []spec.CredentialCapability) []oauthRule {
	var out []oauthRule
	for _, c := range creds {
		if !IsOAuthManaged(c) {
			continue
		}
		rule := oauthRule{
			service: c.Service, required: c.Required, runtime: c.Phase == "runtime",
			tokenHost: compilePattern(c.OAuth.TokenEndpoint.Host), tokenPath: c.OAuth.TokenEndpoint.Path,
		}
		for _, h := range c.OAuth.ResourceHosts {
			rule.resourceHosts = append(rule.resourceHosts, compilePattern(h))
		}
		if c.OAuth.Sentinels != nil {
			rule.sentinels = *c.OAuth.Sentinels
		}
		if c.OAuth.ResponseFields != nil {
			rule.responseFields = *c.OAuth.ResponseFields
		}
		out = append(out, rule)
	}
	return out
}

// tokenEndpointRule returns the session's rule whose token endpoint r is a
// POST to, for the request's current phase.
func tokenEndpointRule(s *Session, r *http.Request, host string, port int) (oauthRule, bool) {
	if r.Method != http.MethodPost {
		return oauthRule{}, false
	}
	atRuntime := s.atRun.Load()
	for _, rule := range s.oauth {
		if rule.runtime != atRuntime || !rule.tokenHost.matches(normalizeHost(host), port) {
			continue
		}
		if rule.tokenPath != "" && r.URL.Path != rule.tokenPath {
			continue
		}
		return rule, true
	}
	return oauthRule{}, false
}

// granted reports whether the run credential carries rule's service. The
// org's stored token is read or written only for a granted run: a Kit
// declaring a service does not by itself reach the org's secret. An optional
// ungranted service is skipped; a required one is an error.
func (p *Proxy) granted(ctx context.Context, s *Session, rule oauthRule) (bool, error) {
	var err error
	switch {
	case p.oauth == nil:
		err = errors.New("no harness secret store is configured")
	case p.resolver == nil:
		err = ErrUnbound
	default:
		_, err = p.resolver.Resolve(ctx, s.run, rule.service)
	}
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrUnbound) && !rule.required:
		return false, nil
	case errors.Is(err, ErrUnbound):
		return false, fmt.Errorf("credential %q is required and not bound for this run", rule.service)
	default:
		return false, fmt.Errorf("credential %q: %w", rule.service, err)
	}
}

// injectOAuth swaps the sentinel access token for the stored real one on a
// request to one of a granted rule's resource hosts. A request not carrying
// the sentinel never touches the store.
func (p *Proxy) injectOAuth(ctx context.Context, s *Session, r *http.Request, host string, port int) error {
	atRuntime := s.atRun.Load()
	for _, rule := range s.oauth {
		if rule.runtime != atRuntime || rule.sentinels.AccessToken == "" ||
			!matchesAny(rule.resourceHosts, normalizeHost(host), port) || !headerCarries(r.Header, rule.sentinels.AccessToken) {
			continue
		}
		ok, err := p.granted(ctx, s, rule)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		current, err := p.oauth.GetHarnessSecret(ctx, s.org, rule.service)
		if err != nil {
			return fmt.Errorf("credential %q has no captured OAuth token: %w", rule.service, err)
		}
		for _, vs := range r.Header {
			for i, v := range vs {
				vs[i] = strings.ReplaceAll(v, rule.sentinels.AccessToken, current.AccessToken)
			}
		}
	}
	return nil
}

// interceptOAuth handles one request to a granted rule's token endpoint
// whole. A refresh carrying the sentinel refresh token goes upstream with
// the stored real one; a login code exchange carries none and passes as
// sent. A successful response is captured to the store and reaches the
// container with sentinels in place of the real tokens, so a refreshed
// token is never returned to it.
func (p *Proxy) interceptOAuth(ctx context.Context, w http.ResponseWriter, r *http.Request, s *Session, rule oauthRule, scheme, target string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxTokenExchange))
	if err != nil {
		http.Error(w, "read token request: "+err.Error(), http.StatusBadGateway)
		return
	}
	var current harnesssecret.Secret
	if sentinel := rule.sentinels.RefreshToken; sentinel != "" && bytes.Contains(body, []byte(sentinel)) {
		current, err = p.oauth.GetHarnessSecret(ctx, s.org, rule.service)
		if err != nil {
			http.Error(w, "credential "+rule.service+" has no captured OAuth token (run the setup terminal first): "+err.Error(), http.StatusBadGateway)
			return
		}
		body = bytes.ReplaceAll(body, []byte(sentinel), []byte(current.RefreshToken))
	}
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, scheme+"://"+target+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "build upstream token request: "+err.Error(), http.StatusBadGateway)
		return
	}
	out.Host = r.Host
	out.Header = r.Header.Clone()
	out.Header.Del("Proxy-Authorization")
	out.Header.Del("Proxy-Connection")
	// The response is parsed and rewritten here, so it must arrive as
	// plain JSON; the transport negotiates and decodes gzip on its own.
	out.Header.Del("Accept-Encoding")
	resp, err := p.transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "upstream token endpoint "+target+": "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenExchange))
	if err != nil {
		http.Error(w, "read token response: "+err.Error(), http.StatusBadGateway)
		return
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		captured, err := rule.capture(s.org, respBody, current)
		if err == nil {
			err = p.oauth.PutHarnessSecret(ctx, captured)
		}
		if err != nil {
			http.Error(w, "capture OAuth token: "+err.Error(), http.StatusBadGateway)
			return
		}
		if respBody, err = rule.sentinelize(respBody); err != nil {
			http.Error(w, "rewrite token response: "+err.Error(), http.StatusBadGateway)
			return
		}
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(respBody)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// responseFieldNames returns the provider's token-response field names,
// defaulting to the OAuth 2.0 ones the rule does not override.
func (rule oauthRule) responseFieldNames() (access, refresh, expiresIn string) {
	access, refresh, expiresIn = "access_token", "refresh_token", "expires_in"
	if rule.responseFields.AccessToken != "" {
		access = rule.responseFields.AccessToken
	}
	if rule.responseFields.RefreshToken != "" {
		refresh = rule.responseFields.RefreshToken
	}
	if rule.responseFields.ExpiresIn != "" {
		expiresIn = rule.responseFields.ExpiresIn
	}
	return access, refresh, expiresIn
}

// capture parses a successful token response into the token set to store.
// A provider that does not rotate refresh tokens omits the field, so the
// previous refresh token is kept.
func (rule oauthRule) capture(org string, body []byte, previous harnesssecret.Secret) (harnesssecret.Secret, error) {
	access, refresh, expiresIn := rule.responseFieldNames()
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return harnesssecret.Secret{}, fmt.Errorf("parse token response: %w", err)
	}
	secret := harnesssecret.Secret{Org: org, Service: rule.service, RefreshToken: previous.RefreshToken, Scopes: previous.Scopes}
	secret.AccessToken, _ = parsed[access].(string)
	if v, ok := parsed[refresh].(string); ok && v != "" {
		secret.RefreshToken = v
	}
	secret.TokenType, _ = parsed["token_type"].(string)
	// A provider may report the granted scopes only on the login exchange, so
	// an absent (or empty) scope keeps the set the previous capture held --
	// the same rule the refresh token follows.
	if v, ok := parsed["scope"].(string); ok && v != "" {
		secret.Scopes = strings.Fields(v)
	}
	if v, ok := parsed[expiresIn].(float64); ok {
		secret.ExpiresAt = time.Now().Add(time.Duration(v) * time.Second)
	}
	if secret.AccessToken == "" {
		return harnesssecret.Secret{}, fmt.Errorf("token response has no %q field", access)
	}
	return secret, nil
}

// sentinelize replaces a captured response's access and refresh tokens with
// the Kit's sentinels. A token field with no declared sentinel cannot be
// hidden, so it fails rather than hand the container a real token.
func (rule oauthRule) sentinelize(body []byte) ([]byte, error) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	access, refresh, _ := rule.responseFieldNames()
	for field, sentinel := range map[string]string{access: rule.sentinels.AccessToken, refresh: rule.sentinels.RefreshToken} {
		if _, ok := parsed[field]; !ok {
			continue
		}
		if sentinel == "" {
			return nil, fmt.Errorf("credential %q declares no sentinel for %q", rule.service, field)
		}
		parsed[field] = sentinel
	}
	return json.Marshal(parsed)
}

func matchesAny(patterns []pattern, host string, port int) bool {
	for _, p := range patterns {
		if p.matches(host, port) {
			return true
		}
	}
	return false
}

func headerCarries(h http.Header, value string) bool {
	for _, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, value) {
				return true
			}
		}
	}
	return false
}
