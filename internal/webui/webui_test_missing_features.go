//go:build ignore

package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexHasResponsiveFeatures(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	reqs := []struct {
		name string
		want string
	}{
		{"mobile-font-padding", "section { min-height: 1.25rem; }"},
		{"table-wrap", "section .table-wrap { position: relative; overflow-x: auto;"},
		{"feed-wraps", "white-space: normal"},
		{"700px-breakpoint", "@media (max-width: 700px)"},
	}
	for _, r := range reqs {
		if !strings.Contains(body, r.want) {
			t.Errorf("missing responsive feature %q: %q", r.name, r.want)
		}
	}
}
