package webui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// fakeAuthorizer records the request it evaluated and answers with a fixed
// decision. It is the chain stand-in the webui tests run against.
type fakeAuthorizer struct {
	decision access.Decision
	last     access.Resource
	lastAct  access.Action
}

func (f *fakeAuthorizer) Authorize(_ access.Principal, a access.Action, r access.Resource, _ access.Context) access.Decision {
	f.lastAct, f.last = a, r
	return f.decision
}

type recordingDenials struct{ recorded []access.Denial }

func (r *recordingDenials) RecordDenial(_ context.Context, d access.Denial) error {
	r.recorded = append(r.recorded, d)
	return nil
}

func (r *recordingDenials) ListDenials(_ context.Context, _ org.OrgID, _ int) ([]access.Denial, error) {
	return nil, errors.New("not needed")
}

func handlerEcho(w http.ResponseWriter, r *http.Request) {
	principal, ok := RequestPrincipal(r.Context())
	if ok {
		w.Header().Set("X-Principal-Org", string(principal.Org))
	}
	w.WriteHeader(http.StatusOK)
}

func newAccessServer(t *testing.T, engine access.Authorizer, denials access.DenialStore) *Server {
	t.Helper()
	return &Server{Access: engine, Principals: nil, Denials: denials, Token: ""}
}

func TestAuthorizeNilEngineServesEverything(t *testing.T) {
	s := &Server{}
	h := s.authorize(http.HandlerFunc(handlerEcho))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nil chain served %d, want 200: the credential check stays the gate", rec.Code)
	}
}

func TestAuthorizeTokenOwnerAllowed(t *testing.T) {
	engine := &fakeAuthorizer{decision: access.Allowed()}
	s := newAccessServer(t, engine, nil)
	h := s.authorize(http.HandlerFunc(handlerEcho))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/bindings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed request served %d, want 200", rec.Code)
	}
	// The token owner is the owner of the default org, and the route
	// derived a read on the binding surface.
	if rec.Header().Get("X-Principal-Org") != string(org.DefaultOrgID) {
		t.Fatalf("principal org = %q, want the default org", rec.Header().Get("X-Principal-Org"))
	}
	if engine.lastAct != access.ActionRead || engine.last.Kind != access.KindBinding {
		t.Fatalf("derived action %q kind %q, want read on bindings", engine.lastAct, engine.last.Kind)
	}
}

func TestAuthorizeDenialIsForbiddenWithoutReason(t *testing.T) {
	engine := &fakeAuthorizer{decision: access.DeniedAt(access.LevelOrg, []string{access.PolicyOrgRead})}
	denials := &recordingDenials{}
	s := newAccessServer(t, engine, denials)
	h := s.authorize(http.HandlerFunc(handlerEcho))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/bindings/b1", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied request served %d, want 403", rec.Code)
	}
	if rec.Body != nil && rec.Body.String() != "forbidden\n" {
		t.Fatalf("denial body = %q, want no reason", rec.Body.String())
	}
	if engine.lastAct != access.ActionDelete {
		t.Fatalf("derived action = %q, want delete", engine.lastAct)
	}
	if len(denials.recorded) != 1 {
		t.Fatalf("recorded %d denials, want 1", len(denials.recorded))
	}
	recorded := denials.recorded[0]
	if recorded.Principal != identity.SystemID || recorded.Action != access.ActionDelete ||
		recorded.Level != access.LevelOrg || len(recorded.Policies) != 1 {
		t.Fatalf("denial record = %+v, want principal, action, level and policies", recorded)
	}
}

func TestAuthorizeApproveRoute(t *testing.T) {
	engine := &fakeAuthorizer{decision: access.Allowed()}
	s := newAccessServer(t, engine, nil)
	h := s.authorize(http.HandlerFunc(handlerEcho))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/bindings/b1/approve", nil))
	if engine.lastAct != access.ActionApprove {
		t.Fatalf("approve route derived action %q, want approve", engine.lastAct)
	}
}

func TestAuthorizeTaskLogRouteReadsLogs(t *testing.T) {
	engine := &fakeAuthorizer{decision: access.Allowed()}
	s := newAccessServer(t, engine, nil)
	h := s.authorize(http.HandlerFunc(handlerEcho))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/7/logs", nil))
	if engine.lastAct != access.ActionReadLogs || engine.last.Kind != access.KindTask || engine.last.ID != "7" {
		t.Fatalf("log route derived %+v %q, want read_logs on task 7", engine.lastAct, engine.last.Kind)
	}
}
