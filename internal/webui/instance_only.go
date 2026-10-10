package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// instanceViewer reports whether r may see instance-wide detail: process
// health, service logs, the loaded config, the setup checklist read from it,
// the skill catalogue and every identity on the instance.
// Only the system org operates the instance; a request with no principal is
// an install without the access chain, whose single operator is that org.
func instanceViewer(r *http.Request) bool {
	p, ok := access.PrincipalFromContext(r.Context())
	return !ok || p.Org == org.DefaultOrgID
}

// instanceOnly serves h to instance viewers and answers not found to anyone
// else, so another org learns nothing about the instance.
func instanceOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !instanceViewer(r) {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}
