// The access chain at the dashboard and API request path: the second of the
// two places that call the Authorizer (docs/prds/orgs-and-access.md, "Where
// it lives"). A request that carries an identity is assembled into a
// principal; the action and resource kind are derived from the route; the
// chain decides; a denial is recorded with the level that decided it and
// reported as forbidden without the reason.
//
// The shared token is the single-operator install's owner of the default org
// (docs/prds/orgs-and-access.md, "Credentials"): the token principal is
// assembled here, not looked up, until principal credentials exist.
package webui

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// TokenOwnerPrincipal is the principal the shared token acts as: the owner
// of the default org, per the single-operator credential rule.
func TokenOwnerPrincipal() access.Principal {
	return access.Principal{
		IdentityID: identity.SystemID,
		Kind:       identity.KindSystem,
		Org:        org.DefaultOrgID,
		Memberships: []org.Membership{
			{IdentityID: identity.SystemID, OrgID: org.DefaultOrgID, Role: org.RoleOwner},
		},
	}
}

// principalContextKey carries the assembled principal of a request.
type principalContextKey struct{}

// WithRequestPrincipal attaches the principal a request was authorized for.
func WithPrincipal(ctx context.Context, p access.Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// RequestPrincipal returns the principal a request was authorized as.
func RequestPrincipal(ctx context.Context) (access.Principal, bool) {
	value, ok := ctx.Value(principalContextKey{}).(access.Principal)
	return value, ok
}

// authorize wraps h with the policy chain. Optional: a server with no
// Authorizer keeps the credential check as the whole gate, which is the
// documented behaviour of an install that has not built the chain.
func (s *Server) authorize(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Access == nil {
			h.ServeHTTP(w, r)
			return
		}
		principal, err := s.requestPrincipal(r.Context())
		if err != nil {
			if wantsDocument(r) {
				s.authPage(w, "Sign-in could not resolve your org.")
				return
			}
			http.Error(w, "principal unavailable", http.StatusServiceUnavailable)
			return
		}
		action, kind, id := accessRequest(r)
		// The record's org is not loaded here: org-filtered lookups keep a
		// record of another org from ever being fetched, and this request's
		// record (when it has an ID) belongs to the caller's org. The
		// workspace is likewise the record's, not the route's.
		resource := access.Resource{Kind: kind, ID: id, Org: principal.Org}
		decision := s.Access.Authorize(principal, action, resource, access.Context{})
		if !decision.Allowed {
			s.recordDenial(r.Context(), principal, action, resource, decision)
			if wantsDocument(r) {
				s.authPage(w, "You do not have permission to do that.")
				return
			}
			// A denial inside the caller's org is forbidden, without the
			// reason (docs/prds/orgs-and-access.md, "Denials").
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

// requestPrincipal assembles the principal a request acts as. With no acting
// identity (the shared token), it is the token owner; with one, it is the
// identity's org and memberships, read over the wire.
func (s *Server) requestPrincipal(ctx context.Context) (access.Principal, error) {
	if acting, ok := ActingIdentity(ctx); ok {
		if s.Principals == nil {
			return access.Principal{}, errPrincipalsUnavailable
		}
		return s.Principals.PrincipalFor(ctx, acting.ID)
	}
	return TokenOwnerPrincipal(), nil
}

func (s *Server) recordDenial(ctx context.Context, p access.Principal, action access.Action, r access.Resource, decision access.Decision) {
	if s.Denials == nil {
		return
	}
	denial := access.Denial{
		Principal: p.IdentityID, Org: p.Org, Action: action,
		Kind: r.Kind, ResourceID: r.ID, Level: decision.Level,
		Policies: decision.Policies,
	}
	if err := s.Denials.RecordDenial(ctx, denial); err != nil && s.Log != nil {
		s.Log.Error("record access denial", "err", err)
	}
}

// errPrincipalsUnavailable is the answer of a dashboard wired with the chain
// but without the principal assembly, which is a composition error: the two
// are wired together or not at all.
var errPrincipalsUnavailable = &notWiredError{}

type notWiredError struct{}

func (*notWiredError) Error() string { return "webui: principal assembly not wired" }

// accessRequest derives the action, resource kind and record key of one API
// request from its method and path. Unmapped routes read the dashboard
// itself, so a new route is never wider than read until it is mapped.
func accessRequest(r *http.Request) (access.Action, access.ResourceKind, string) {
	action := actionOf(r.Method)
	path := r.URL.Path
	if a, kind, id, ok := routeOverride(action, path); ok {
		return a, kind, id
	}
	return action, kindOf(path), segmentValue(path, 2)
}

// actionOf maps the HTTP method onto the action vocabulary. POST is create:
// the route refinements lift the routes whose write is something else.
func actionOf(method string) access.Action {
	switch method {
	case http.MethodPost:
		return access.ActionCreate
	case http.MethodPut, http.MethodPatch:
		return access.ActionUpdate
	case http.MethodDelete:
		return access.ActionDelete
	}
	return access.ActionRead
}

// routeOverride returns the action, kind and record key a specific route
// shape carries, or ok=false when the method-and-kind fallback applies.
func routeOverride(action access.Action, path string) (access.Action, access.ResourceKind, string, bool) {
	switch {
	case action == access.ActionCreate && matchSegment(path, "/api/bindings/", "/approve"):
		return access.ActionApprove, access.KindBinding, segmentValue(path, 2), true
	case matchSegment(path, "/api/tasks/", "/logs"):
		return access.ActionReadLogs, access.KindTask, segmentValue(path, 2), true
	case matchPrefix(path, "/api/identities"):
		return actionManage(action), access.KindIdentity, segmentValue(path, 2), true
	case matchPrefix(path, "/api/control-plane/resources"):
		return actionManage(action), access.KindPolicy, segmentValue(path, 3), true
	case matchPrefix(path, "/api/chat"):
		if action == access.ActionCreate {
			return access.ActionRun, access.KindWorkflow, "", true
		}
		return access.ActionRead, access.KindDashboard, "", true
	case matchSegment(path, "/api/sources/", "/secret"):
		return access.ActionUpdate, access.KindSecret, segmentValue(path, 2), true
	case matchPrefix(path, "/api/channels/"):
		return actionManage(action), access.KindDashboard, segmentValue(path, 2), true
	case matchPrefix(path, "/api/config"), matchPrefix(path, "/api/logs"):
		return access.ActionRead, access.KindDashboard, "", true
	}
	return action, "", "", false
}

// kindOf maps a collection route onto the resource kind it addresses.
func kindOf(path string) access.ResourceKind {
	switch {
	case matchPrefix(path, "/api/tasks"):
		return access.KindTask
	case matchPrefix(path, "/api/event-types"):
		return access.KindEventType
	case matchPrefix(path, "/api/mappings"):
		return access.KindMapping
	case matchPrefix(path, "/api/bindings"):
		return access.KindBinding
	case matchPrefix(path, "/api/sources"):
		return access.KindSource
	case matchPrefix(path, "/api/work-requests"):
		return access.KindTask
	}
	return access.KindDashboard
}

// actionManage lifts a create or update on a managed surface to the manage
// action the role vocabulary carries.
func actionManage(action access.Action) access.Action {
	switch action {
	case access.ActionCreate, access.ActionUpdate:
		return access.ActionManageIdentities
	case access.ActionDelete:
		return access.ActionDelete
	}
	return action
}

// segmentValue returns the value of the n-th (0-based) segment of an API
// path, or "" when the path is shorter: /api/tasks/7/changes -> 7 is segment
// 2.
func segmentValue(path string, n int) string {
	segments := splitPath(path)
	if n < len(segments) {
		return segments[n]
	}
	return ""
}

func splitPath(path string) []string {
	var out []string
	for segment := range strings.SplitSeq(path, "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

func matchPrefix(path, prefix string) bool {
	return strings.HasPrefix(path, prefix+"/") || path == prefix
}

func matchSegment(path, prefix, suffix string) bool {
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix)
}

// The log the authorize wrapper reports assembly failures with.
var _ = slog.Default //nolint:unused // logging stays on s.Log
