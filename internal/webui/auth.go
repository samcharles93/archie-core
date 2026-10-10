package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// tokenCookie carries the dashboard token once it has been exchanged from the
// URL, so the token is not left sitting in browser history or referrers.
const (
	tokenCookie = "archie_ui"
	// providerTokenCookie holds the provider's own access token after a browser
	// sign-in. It is not a session archie issued: the token belongs to the
	// provider and is verified on every request exactly as a presented header is,
	// so nothing archie stores can outlive the provider's decision.
	providerTokenCookie = "archie_provider_token"
	// The sign-in flow's state and PKCE verifier live in short-lived cookies for
	// the duration of the redirect, scoped to the callback path.
	loginStateCookie    = "archie_login_state"
	loginVerifierCookie = "archie_login_verifier"
	loginCookieMaxAge   = 600
	// loginPath is where a browser signs in, and the path a refused browser is
	// sent to. loginCallbackPath must match the redirect URI registered at the
	// provider: the provider returns the browser to the path it was given.
	loginPath          = "/oauth2/login"
	loginCallbackPath  = "/oauth2/callback"
	loginRoute         = "GET " + loginPath
	loginCallbackRoute = "GET " + loginCallbackPath
)

// IsLoopback reports whether a listen address is reachable only from this
// machine. Loopback binds need no token.
func IsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		host = strings.TrimSpace(listen)
	}
	// An empty host is the wildcard, not loopback: ":8484" binds every
	// interface, so it must not pass the loopback exemption.
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// DashboardURL renders a listen address as an openable URL, with localhost
// for wildcard hosts. It deliberately carries no credential: a URL is safe to
// log and paste, so the dashboard token can only be read from the file the
// operator named, never from a printed link.
func DashboardURL(listen string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		host, port = "", strings.TrimPrefix(strings.TrimSpace(listen), ":")
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

// LoadOrCreateToken returns the dashboard token, generating and persisting one
// on first use. The token is created rather than configured so a non-loopback
// bind cannot be brought up unprotected by omission: there is no setup step to
// forget.
func LoadOrCreateToken(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("webui: read token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("webui: generate token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("webui: token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("webui: write token: %w", err)
	}
	return tok, nil
}

// requireToken checks the request's credential: a provider-issued bearer
// token when an identity provider is configured, else the shared token. A
// ?t= token is moved into a cookie and the request redirected.
func (s *Server) requireToken(h http.Handler) http.Handler {
	if s.Authenticate != nil {
		return s.requireIdentity(h)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" {
			h.ServeHTTP(w, r)
			return
		}
		if tok := r.URL.Query().Get("t"); tok != "" {
			if !tokenEqual(tok, s.Token) {
				if wantsDocument(r) {
					s.authPage(w, http.StatusUnauthorized, "That token was not accepted.")
					return
				}
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "unauthorised", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     tokenCookie,
				Value:    tok,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				Secure:   r.TLS != nil,
			})
			clean := *r.URL
			q := clean.Query()
			q.Del("t")
			clean.RawQuery = q.Encode()
			http.Redirect(w, r, sameOriginPath(clean.Path, clean.RawQuery), http.StatusSeeOther)
			return
		}

		if c, err := r.Cookie(tokenCookie); err == nil && tokenEqual(c.Value, s.Token) {
			h.ServeHTTP(w, r)
			return
		}
		if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") &&
			tokenEqual(strings.TrimPrefix(hdr, "Bearer "), s.Token) {
			h.ServeHTTP(w, r)
			return
		}

		if wantsDocument(r) {
			s.authPage(w, http.StatusUnauthorized, "")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "unauthorised: open the dashboard URL archied logged at startup", http.StatusUnauthorized)
	})
}

// requireIdentity authenticates a provider bearer token and attaches the
// identity it resolves to.
func (s *Server) requireIdentity(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential, ok := bearerCredential(r)
		if !ok {
			s.refuseUnidentified(w, r, identity.ErrNoCredential)
			return
		}
		value, err := s.Authenticate(r.Context(), credential)
		if err != nil {
			s.refuseUnidentified(w, r, err)
			return
		}
		h.ServeHTTP(w, r.WithContext(WithActingIdentity(r.Context(), value)))
	})
}

// bearerCredential returns the request's bearer token or provider cookie.
func bearerCredential(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		if token := strings.TrimSpace(header[len(prefix):]); token != "" {
			return token, true
		}
	}
	if c, err := r.Cookie(providerTokenCookie); err == nil && strings.TrimSpace(c.Value) != "" {
		return c.Value, true
	}
	return "", false
}

// handleLogin sends a browser to the provider with the audience requested at the
// authorization endpoint, and keeps the state and PKCE verifier the callback needs
// in cookies scoped to the callback path.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.Login == nil {
		http.NotFound(w, r)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	url, verifier, err := s.Login.AuthCodeURL(r.Context(), state)
	if err != nil {
		s.refuseUnidentified(w, r, err)
		return
	}
	setFlowCookie(w, r, loginStateCookie, state)
	setFlowCookie(w, r, loginVerifierCookie, verifier)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// handleCallback completes the flow. It checks the state it issued, exchanges the
// code with the PKCE verifier it issued, and then resolves the resulting token
// through the same check every other request goes through -- so a browser sign-in
// cannot be accepted on terms a presented token would be refused on.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if s.Login == nil || s.Authenticate == nil {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	if providerErr := query.Get("error"); providerErr != "" {
		s.refuseUnidentified(w, r, fmt.Errorf("%w: provider returned %s", identity.ErrCredentialRejected, providerErr))
		return
	}
	state, stateErr := r.Cookie(loginStateCookie)
	if stateErr != nil || !tokenEqual(state.Value, query.Get("state")) {
		s.refuseUnidentified(w, r, fmt.Errorf("%w: sign-in state did not match", identity.ErrCredentialRejected))
		return
	}
	verifier, verifierErr := r.Cookie(loginVerifierCookie)
	if verifierErr != nil || strings.TrimSpace(verifier.Value) == "" {
		s.refuseUnidentified(w, r, fmt.Errorf("%w: sign-in verifier is missing", identity.ErrCredentialRejected))
		return
	}
	session, err := s.Login.Exchange(r.Context(), query.Get("code"), verifier.Value)
	if err != nil {
		s.refuseUnidentified(w, r, err)
		return
	}
	if _, err := s.Authenticate(r.Context(), session.Token); err != nil {
		s.refuseUnidentified(w, r, err)
		return
	}
	clearFlowCookie(w, r, loginStateCookie)
	clearFlowCookie(w, r, loginVerifierCookie)
	http.SetCookie(w, &http.Cookie{
		Name: providerTokenCookie, Value: session.Token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func setFlowCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/oauth2",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		MaxAge: loginCookieMaxAge,
	})
}

func clearFlowCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/oauth2",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: -1,
	})
}

// randomToken returns a fresh URL-safe nonce for a sign-in's state.
func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// refuseUnidentified answers 401 for missing or invalid credentials and 403
// for an identity that may not act.
func (s *Server) refuseUnidentified(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	allowed := errors.Is(err, identity.ErrIdentityInactive) || errors.Is(err, identity.ErrSubjectUnbound)
	status := http.StatusUnauthorized
	reason := "That credential was not accepted."
	switch {
	case errors.Is(err, identity.ErrProviderUnavailable):
		// Signing in again cannot help until the provider is back.
		http.Error(w, "The sign-in provider is unavailable. Try again shortly.", http.StatusServiceUnavailable)
		return
	case errors.Is(err, identity.ErrIdentityInactive):
		status, reason = http.StatusForbidden, "That identity may not act."
	case errors.Is(err, identity.ErrSubjectUnbound):
		status, reason = http.StatusForbidden, "That credential is not bound to an identity here."
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="archie"`)
	}
	if !allowed && wantsDocument(r) {
		// Send browsers to sign in, unless signing in again cannot help.
		if s.Login != nil {
			http.Redirect(w, r, loginPath, http.StatusSeeOther)
			return
		}
		s.authPage(w, http.StatusUnauthorized, reason)
		return
	}
	http.Error(w, reason, status)
}

type actingIdentityContextKey struct{}

// WithActingIdentity returns a context carrying the identity a request resolved
// to, so a handler can attribute what it does without re-verifying anything.
func WithActingIdentity(ctx context.Context, value identity.Identity) context.Context {
	return context.WithValue(ctx, actingIdentityContextKey{}, value)
}

// ActingIdentity returns the identity a request resolved to, if it resolved to
// one. A false result means the request was not authenticated, which is a
// different fact from an authenticated request whose actor is unrecorded.
func ActingIdentity(ctx context.Context) (identity.Identity, bool) {
	value, ok := ctx.Value(actingIdentityContextKey{}).(identity.Identity)
	return value, ok
}

// wantsDocument reports whether a request is a browser navigation rather than an
// API or stream call. A navigation sends text/html in Accept; fetch() sends */*
// or a JSON type, and EventSource sends text/event-stream.
func wantsDocument(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// authPage serves the self-contained page for pasting the access token,
// with code: 401 for a credential failure, 503 for an outage.
func (s *Server) authPage(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.WriteHeader(code)
	notice := ""
	if reason != "" {
		notice = `<p class="error">` + reason + `</p>`
	}
	_, _ = io.WriteString(w, strings.Replace(authPageHTML, "<!--auth-error-->", notice, 1))
}

// authPageHTML is the unauthenticated document response. The form reuses the
// ?t= exchange, so its action is the same path a startup URL uses.
const authPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Archie dashboard</title>
<style>
:root { color-scheme: dark light; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; font: 15px/1.5 system-ui, sans-serif; background: #0b0b10; color: #e8e8ee; }
main { width: min(26rem, calc(100% - 2rem)); padding: 2rem; border: 1px solid #2a2a38; border-radius: 0.75rem; background: #14141c; }
h1 { margin: 0 0 0.75rem; font-size: 1.25rem; }
p { margin: 0 0 1rem; color: #b9b9c8; }
label { display: block; margin-bottom: 0.5rem; font-weight: 600; }
input { width: 100%; box-sizing: border-box; padding: 0.6rem 0.7rem; border: 1px solid #3a3a4c; border-radius: 0.5rem; background: #0b0b10; color: inherit; font: inherit; }
button { margin-top: 1rem; width: 100%; padding: 0.6rem 0.7rem; border: 0; border-radius: 0.5rem; background: #7c5cff; color: #fff; font: inherit; font-weight: 600; cursor: pointer; }
.error { padding: 0.6rem 0.7rem; border: 1px solid #7f2b3b; border-radius: 0.5rem; background: #2a1118; color: #ffb4c0; }
.hint { margin: 1rem 0 0; font-size: 0.85rem; }
</style>
</head>
<body>
<main>
<h1>Archie dashboard</h1>
<p>This dashboard needs its access token before it will load.</p>
<!--auth-error-->
<form method="get" action="/">
<label for="t">Access token</label>
<input id="t" name="t" type="password" autocomplete="off" autofocus required>
<button type="submit">Open the dashboard</button>
</form>
<p class="hint">Paste the token from the dashboard URL archied printed at startup, or the token your deployment issued.</p>
</main>
</body>
</html>
`

// sameOriginPath rebuilds a redirect target, collapsing a leading "//" so it
// cannot redirect off-host.
func sameOriginPath(path, rawQuery string) string {
	// URL.Path has already been decoded by net/http. Treat backslashes as
	// separators before checking for a protocol-relative path, because browsers
	// do the same when interpreting a redirect Location.
	path = strings.ReplaceAll(path, "\\", "/")
	for strings.HasPrefix(path, "//") {
		path = path[1:]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Re-encoding through URL.Path preserves decoded path characters such as
	// '%', '?' and '#' as path data instead of reparsing them as URL syntax.
	return (&url.URL{Path: path, RawQuery: rawQuery}).RequestURI()
}

func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
