package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
)

// Installing, accepting, enabling or removing an extension runs or stops a
// process on the host, so every extensions route is policy administration, not
// a dashboard read.
func TestExtensionRoutesAreAdministration(t *testing.T) {
	tests := []struct {
		method, path string
		action       access.Action
	}{
		{http.MethodGet, "/api/extensions", access.ActionRead},
		{http.MethodPost, "/api/extensions", access.ActionManageIdentities},
		{http.MethodPost, "/api/extensions/bws/accept", access.ActionManageIdentities},
		{http.MethodPut, "/api/extensions/bws/enabled", access.ActionManageIdentities},
		{http.MethodDelete, "/api/extensions/bws", access.ActionDelete},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			action, kind, _ := accessRequest(httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil))
			if action != tt.action || kind != access.KindPolicy {
				t.Fatalf("accessRequest = %q on %q, want %q on %q", action, kind, tt.action, access.KindPolicy)
			}
		})
	}
}
