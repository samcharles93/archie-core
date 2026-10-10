package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// operator is the person owner the upgrade creates and the shared-token
// session acts as.
func operator() access.Principal {
	return access.Principal{
		IdentityID: identity.OperatorID(),
		Org:        org.DefaultOrgID,
		Memberships: []org.Membership{
			{IdentityID: identity.OperatorID(), OrgID: org.DefaultOrgID, Role: org.RoleOwner},
		},
	}
}

// stubPrincipals answers the operator's own principal and a bare one for
// anyone else.
func stubPrincipals(haveOperator bool) access.PrincipalSource {
	return operatorPrincipals{haveOperator: haveOperator}
}

type operatorPrincipals struct{ haveOperator bool }

func (s operatorPrincipals) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	if s.haveOperator && id == identity.OperatorID() {
		return operator(), nil
	}
	return access.Principal{IdentityID: id, Org: org.DefaultOrgID}, nil
}

type allowAll struct{}

func (allowAll) Authorize(access.Principal, access.Action, access.Resource, access.Context) access.Decision {
	return access.Allowed()
}

func (allowAll) Ready() bool { return true }

// notReadyChain is an authorizer whose engine has not loaded: every decision
// is the not-loaded refusal, which no policy evaluated.
type notReadyChain struct{}

func (notReadyChain) Authorize(access.Principal, access.Action, access.Resource, access.Context) access.Decision {
	return access.Unavailable()
}

func (notReadyChain) Ready() bool { return false }

type recordingTokens struct{ owner identity.IdentityID }

// AddPersonalToken mirrors the State Store's rule: only a signed-in person's
// principal may own a personal token.
func (t *recordingTokens) AddPersonalToken(ctx context.Context, _ identity.Subject) error {
	p, ok := access.PrincipalFromContext(ctx)
	if !ok || p.IdentityID == identity.SystemID || p.Role("") != org.RoleOwner {
		return identity.ErrInvalid
	}
	t.owner = p.IdentityID
	return nil
}

func (*recordingTokens) ListPersonalTokens(context.Context) ([]identity.PersonalToken, error) {
	return nil, nil
}

func (*recordingTokens) RevokePersonalToken(context.Context, string) error { return nil }

// TestSharedTokenPrincipal pins who the dashboard acts as when no provider
// signed anyone in: the org-sys owner person when the upgrade made one, the
// system owner otherwise.
func TestSharedTokenPrincipal(t *testing.T) {
	tests := []struct {
		name       string
		principals access.PrincipalSource
		want       identity.IdentityID
	}{
		{"the operator person owns the shared session", stubPrincipals(true), identity.OperatorID()},
		{"with no operator the system owner remains", stubPrincipals(false), identity.SystemID},
		{"with no principal source the system owner remains", nil, identity.SystemID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{Principals: tt.principals}
			p, err := s.requestPrincipal(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if p.IdentityID != tt.want {
				t.Fatalf("principal = %q, want %q", p.IdentityID, tt.want)
			}
		})
	}
}

// TestSharedTokenOperatorPostsPersonalToken drives the whole dashboard path:
// the shared token resolves to the operator, the token route accepts it where
// the system principal was refused, and a write's audit names the operator.
func TestSharedTokenOperatorPostsPersonalToken(t *testing.T) {
	tokens := &recordingTokens{}
	s := &Server{
		Token:          "shared",
		Access:         allowAll{},
		Principals:     stubPrincipals(true),
		PersonalTokens: tokens,
	}
	handler := s.requireToken(s.authorize(http.HandlerFunc(s.handlePersonalTokenCreate)))
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer shared")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/tokens = %d, body %s", rec.Code, rec.Body.String())
	}
	if tokens.owner != identity.OperatorID() {
		t.Fatalf("token owner = %q, want the operator", tokens.owner)
	}

	audit, err := webAudit(access.WithPrincipal(t.Context(), operator()))
	if err != nil {
		t.Fatal(err)
	}
	if audit.ActorID != identity.OperatorID() {
		t.Fatalf("audit actor = %q, want the operator", audit.ActorID)
	}
}

// TestAuthorizeRefusesWithoutAChain pins the gate for a server with no chain
// and for a chain that has not loaded: the request is refused rather than
// served as the system org, and nothing is written.
func TestAuthorizeRefusesWithoutAChain(t *testing.T) {
	tests := []struct {
		name                 string
		srv                  *Server
		method, path, accept string
		body                 string
	}{
		{name: "no chain", srv: &Server{}, method: http.MethodPost, path: "/api/tokens"},
		{name: "chain not loaded", srv: &Server{Access: notReadyChain{}}, method: http.MethodPost, path: "/api/tokens"},
		// An outage is not a credential failure, so the browser notice is 503
		// too.
		{name: "chain not loaded, browser", srv: &Server{Access: notReadyChain{}}, method: http.MethodGet, path: "/tasks", accept: "text/html"},
		// The health report still says why, naming only the chain.
		{name: "chain not loaded, health detail", srv: &Server{Access: notReadyChain{}}, method: http.MethodGet, path: healthDetailedRoute, body: `"name":"access_policies"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := &recordingTokens{}
			tt.srv.Token = "shared"
			tt.srv.Principals = stubPrincipals(true)
			tt.srv.PersonalTokens = tokens
			handler := tt.srv.requireToken(tt.srv.authorize(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("the handler must not be reached")
			})))
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer shared")
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.body) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.body)
			}
			if tokens.owner != "" {
				t.Fatalf("token owner = %q, want no write", tokens.owner)
			}
		})
	}
}

func TestAdministrationRoutes(t *testing.T) {
	tests := []struct {
		method, path string
		action       access.Action
	}{
		{http.MethodGet, "/api/extensions", access.ActionRead},
		{http.MethodPost, "/api/extensions", access.ActionManageIdentities},
		{http.MethodPost, "/api/extensions/bws/accept", access.ActionManageIdentities},
		{http.MethodPut, "/api/extensions/bws/enabled", access.ActionManageIdentities},
		{http.MethodDelete, "/api/extensions/bws", access.ActionDelete},
		{http.MethodGet, "/api/access/policies", access.ActionRead},
		{http.MethodPut, "/api/access/policies", access.ActionManagePolicies},
		{http.MethodDelete, "/api/access/policies", access.ActionManagePolicies},
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

func TestPersonalTokensWithoutPerson(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			s := &Server{PersonalTokens: &recordingTokens{}}
			req := httptest.NewRequestWithContext(access.WithPrincipal(t.Context(), access.SharedTokenOwner()), method, "/api/tokens", nil)
			rec := httptest.NewRecorder()
			switch method {
			case http.MethodGet:
				s.handlePersonalTokensList(rec, req)
			case http.MethodPost:
				s.handlePersonalTokenCreate(rec, req)
			case http.MethodDelete:
				s.handlePersonalTokenRevoke(rec, req)
			}
			if method == http.MethodGet {
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":false`) {
					t.Fatalf("list without person = %d %s", rec.Code, rec.Body.String())
				}
			} else if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != "Personal tokens need a signed-in person." {
				t.Fatalf("mutation without person = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
