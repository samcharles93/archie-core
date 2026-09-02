package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleConfigRepoUpdateAppliesViaSeam(t *testing.T) {
	srv := newTestServer(t)
	type call struct {
		owner, name, field string
		value              any
	}
	var got call
	srv.UpdateRepoField = func(_ context.Context, owner, name, field string, value any) error {
		got = call{owner, name, field, value}
		return nil
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config/repos/acme/widget",
		strings.NewReader(`{"field": "allow_concurrent", "value": true}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got != (call{"acme", "widget", "allow_concurrent", true}) {
		t.Errorf("seam called with %+v", got)
	}
}

// TestHandleConfigRepoUpdateRejectsUneditableFields proves the handler
// itself refuses a field outside RepoEditableFields before ever calling the
// seam -- Gate, Protect, and every other repo field stay file-edited.
func TestHandleConfigRepoUpdateRejectsUneditableFields(t *testing.T) {
	srv := newTestServer(t)
	called := false
	srv.UpdateRepoField = func(context.Context, string, string, string, any) error {
		called = true
		return nil
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config/repos/acme/widget",
		strings.NewReader(`{"field": "gate", "value": [["rm", "-rf", "/"]]}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if called {
		t.Error("UpdateRepoField was called for a field outside RepoEditableFields")
	}
}

func TestHandleConfigRepoUpdateUnavailableMapsTo503(t *testing.T) {
	srv := newTestServer(t)
	srv.UpdateRepoField = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config/repos/acme/widget",
		strings.NewReader(`{"field": "allow_concurrent", "value": true}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestHandleConfigRepoUpdateSeamErrorMapsTo400(t *testing.T) {
	srv := newTestServer(t)
	srv.UpdateRepoField = func(context.Context, string, string, string, any) error {
		return ErrConfigUpdateInvalid
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config/repos/acme/widget",
		strings.NewReader(`{"field": "max_retries", "value": 5}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
