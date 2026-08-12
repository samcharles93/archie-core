package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// tokenCookie carries the dashboard token once it has been exchanged from the
// URL, so the token is not left sitting in browser history or referrers.
const tokenCookie = "archie_ui"

// IsLoopback reports whether a listen address is reachable only from this
// machine.
//
// Loopback binds need no token. Archie's agent already runs shell, write and
// edit tools on the host, so anyone with local access has the capability
// regardless; a lock here would only obstruct the operator. What a token
// protects is *exposure* -- an instance reachable from a network -- so that is
// the only case where one is required.
func IsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		host = strings.TrimSpace(listen)
	}
	switch host {
	case "", "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// DashboardURL renders a listen address as a URL a human can actually open.
//
// A wildcard bind ("0.0.0.0:8484", ":8484", "[::]:8484") is a valid thing to
// listen on but not a valid thing to visit, and printing it verbatim gives the
// operator a link that goes nowhere. The host is substituted for localhost,
// which is correct for the machine reading the log.
func DashboardURL(listen, token string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		host, port = "", strings.TrimPrefix(strings.TrimSpace(listen), ":")
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	url := "http://" + net.JoinHostPort(host, port) + "/"
	if token != "" {
		url += "?t=" + token
	}
	return url
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

// requireToken wraps h with token authentication. A zero token disables the
// check entirely, which is how loopback binds stay frictionless.
//
// The token may arrive as ?t=... once; it is then moved into a
// SameSite=Strict, HttpOnly cookie and the caller redirected to the clean URL.
// One click on the URL archied logs is the whole setup.
// The token is read per request rather than captured when the handler is
// built, so a Server whose Token is set after Handler() is still protected.
func (s *Server) requireToken(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" {
			h.ServeHTTP(w, r)
			return
		}
		if tok := r.URL.Query().Get("t"); tok != "" {
			if !tokenEqual(tok, s.Token) {
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

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, "unauthorised: open the dashboard URL archied logged at startup", http.StatusUnauthorized)
	})
}

// sameOriginPath rebuilds a redirect target from a request path and raw
// query, collapsing any leading "//" down to a single slash first.
//
// A path of "//evil.example/x" is a valid http.Redirect Location that
// browsers treat as protocol-relative, sending the client off-host. Since
// this path is echoed back from the incoming request URL, an attacker can
// choose it, so it must never reach http.Redirect verbatim.
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
