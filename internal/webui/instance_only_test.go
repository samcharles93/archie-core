package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// Instance-wide detail reaches the system org and an install without the
// access chain, and reads as absent to every other org.
func TestInstanceOnlyHidesTheInstanceFromOtherOrgs(t *testing.T) {
	served := instanceOnly(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, tt := range []struct {
		name      string
		principal *access.Principal
		want      int
	}{
		{"the system org", &access.Principal{Org: org.DefaultOrgID}, http.StatusOK},
		{"no access chain", nil, http.StatusOK},
		{"another org", &access.Principal{Org: "acme"}, http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.principal != nil {
				ctx = access.WithPrincipal(ctx, *tt.principal)
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/logs", nil)
			w := httptest.NewRecorder()
			served(w, r)
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d", w.Code, tt.want)
			}
		})
	}
}
