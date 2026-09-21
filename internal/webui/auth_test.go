package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireToken_RedirectRejectsProtocolRelativePath(t *testing.T) {
	s := &Server{Token: "secret-token"}
	h := s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://archie.local//evil.example//x?t=secret-token", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	loc := rec.Header().Get("Location")
	if strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
		t.Fatalf("Location %q is protocol-relative: open redirect to an attacker-controlled host", loc)
	}
}

func TestSameOriginPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		rawQuery string
		want     string
	}{
		{name: "protocol relative", path: "//evil.example/x", want: "/evil.example/x"},
		{name: "backslash protocol relative", path: "/\\\\evil.example/x", want: "/evil.example/x"},
		{name: "double backslash protocol relative", path: "\\\\evil.example\\x", want: "/evil.example/x"},
		{name: "ordinary path and query", path: "/dashboard", rawQuery: "foo=bar", want: "/dashboard?foo=bar"},
		{name: "encoded percent", path: "/foo%bar", want: "/foo%25bar"},
		{name: "encoded question mark", path: "/foo?bar", want: "/foo%3Fbar"},
		{name: "encoded hash", path: "/foo#bar", want: "/foo%23bar"},
		{name: "encoded slash", path: "/foo/bar", want: "/foo/bar"},
		{name: "encoded backslash", path: "/foo\\bar", want: "/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameOriginPath(tt.path, tt.rawQuery); got != tt.want {
				t.Fatalf("sameOriginPath(%q, %q) = %q, want %q", tt.path, tt.rawQuery, got, tt.want)
			}
		})
	}
}

func TestRequireToken_RedirectStripsTokenFromCleanURL(t *testing.T) {
	s := &Server{Token: "secret-token"}
	h := s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://archie.local/dashboard?t=secret-token&foo=bar", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "t=secret-token") {
		t.Fatalf("Location %q still carries the token", loc)
	}
	if loc != "/dashboard?foo=bar" {
		t.Fatalf("Location = %q, want /dashboard?foo=bar", loc)
	}
}

// An unauthenticated browser navigation must land on something a human can act
// on. Everything -- index.html, the assets, every API -- sits behind
// requireToken, so a raw text 401 left a bookmarked URL with no way forward and
// no way to reach the dashboard's own authentication banner.
func TestRequireToken_ServesAnAuthPageToDocumentRequests(t *testing.T) {
	s := &Server{Token: "secret-token"}
	h := s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an unauthenticated document request reached the protected handler")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://archie.local/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; an auth page served as 200 is cached and reads as success to a status-only check", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{"<form", `action="/"`, `name="t"`, "token"} {
		if !strings.Contains(body, want) {
			t.Fatalf("auth page is missing %q:\n%s", want, body)
		}
	}
}

// A rejected token posts back through the same exchange, so the page has to
// say so and offer another try rather than answering a document with text.
func TestRequireToken_AuthPageReportsARejectedToken(t *testing.T) {
	s := &Server{Token: "secret-token"}
	h := s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request with a bad token reached the protected handler")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://archie.local/?t=wrong", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "not accepted") {
		t.Fatalf("auth page does not report the rejected token:\n%s", body)
	}
}

// API and event-stream callers read a status, not a page: classifyActionError
// maps 401 to "session-expired", and an HTML body on an EventSource error would
// never be seen. The document/API split is the contract this guards.
func TestRequireToken_KeepsTextUnauthorisedForAPIRequests(t *testing.T) {
	tests := []struct {
		name   string
		accept string
	}{
		{name: "json api", accept: "application/json"},
		{name: "fetch default", accept: "*/*"},
		{name: "event stream", accept: "text/event-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{Token: "secret-token"}
			h := s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("an unauthenticated API request reached the protected handler")
			}))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://archie.local/api/tasks", nil)
			req.Header.Set("Accept", tt.accept)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Fatalf("Content-Type = %q, want text/plain", ct)
			}
			if body := rec.Body.String(); !strings.Contains(body, "unauthorised") {
				t.Fatalf("body = %q, want the plain unauthorised text", body)
			}
		})
	}
}
