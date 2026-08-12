package webui

import (
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

	req := httptest.NewRequest(http.MethodGet, "http://archie.local//evil.example//x?t=secret-token", nil)
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

	req := httptest.NewRequest(http.MethodGet, "http://archie.local/dashboard?t=secret-token&foo=bar", nil)
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
