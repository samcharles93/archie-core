package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
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
				if wantsDocument(r) {
					s.authPage(w, "That token was not accepted.")
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
			s.authPage(w, "")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "unauthorised: open the dashboard URL archied logged at startup", http.StatusUnauthorized)
	})
}

// wantsDocument reports whether a request is a browser navigation rather than an
// API or stream call. A navigation sends text/html in Accept; fetch() sends */*
// or a JSON type, and EventSource sends text/event-stream.
func wantsDocument(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// authPage answers an unauthenticated or rejected browser navigation with a page
// a human can act on: the access token is pasted once, the existing ?t=
// exchange sets the cookie, and the redirect lands on the dashboard.
//
// The page is deliberately self-contained. index.html, the bundle and every
// other asset sit behind requireToken too, so a page that referenced anything
// would render unstyled for exactly the visitor who needs it.
//
// The status stays 401: a login page served as 200 is cached, and reads as
// success to anything that only checks the status.
func (s *Server) authPage(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.WriteHeader(http.StatusUnauthorized)
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
