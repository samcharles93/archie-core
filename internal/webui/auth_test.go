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
