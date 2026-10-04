package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// The control-plane resources the harness page reads. The kinds are spelled
// here rather than imported from internal/app/controlplane so the dashboard
// package stays a leaf: api_workflow_enablement.go does the same for
// "workflow-enablement".
const (
	harnessBindingsKind = "credential-bindings"
	harnessProfilesKind = "agent-profiles"
)

// harnessBindingDoc is the part of one credential-bindings entry
// (controlplane.credentialBinding) this page reads: the service it binds.
// The secret reference is decoded too so a malformed document is rejected
// rather than silently read as service-only, but it is never resolved here.
type harnessBindingDoc struct {
	Service string `json:"service"`
	Org     string `json:"org,omitempty"`
	Secret  struct {
		Engine string `json:"engine,omitempty"`
		Key    string `json:"key,omitempty"`
	} `json:"secret"`
}

// harnessProfileDoc is the part of one agent-profiles entry
// (controlplane.agentProfile) this page reads: the Kit a setup terminal can
// be opened for.
type harnessProfileDoc struct {
	Kit string `json:"kit"`
}

// HarnessTerminal opens a setup session to a Kit profile's container PTY.
// Nil answers 503.
type HarnessTerminal interface {
	Open(ctx context.Context, orgID, profile string) (io.ReadWriteCloser, error)
}

// harnessBindingView is one credential binding as the page renders it: the
// service, whether an OAuth token set has been captured, and that set's
// expiry, scopes and capture time. Access and refresh tokens are never in
// the shape -- the page reports capture state, not credentials.
type harnessBindingView struct {
	Service   string     `json:"service"`
	Captured  bool       `json:"captured"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Scopes    []string   `json:"scopes,omitempty"`
}

// harnessView is the page's whole read: the bindings, the Kit profiles a
// setup terminal can target, and whether this process can open one.
type harnessView struct {
	Terminal bool                 `json:"terminal"`
	Bindings []harnessBindingView `json:"bindings"`
	Profiles []string             `json:"profiles"`
}

// handleHarnessBindings lists the org's credential bindings joined with
// their captured OAuth state. It requires both contracts the join reads: an
// unconfigured process answers 503 rather than rendering a page that claims
// the org has no bindings, which it cannot know.
func (s *Server) handleHarnessBindings(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil || s.HarnessSecrets == nil {
		http.Error(w, "harness bindings not configured", http.StatusServiceUnavailable)
		return
	}
	orgID := string(org.OrgFromContext(r.Context()))
	bindings, err := s.harnessBindings(r.Context(), orgID)
	if err != nil {
		s.logf("list harness bindings", "err", err)
		http.Error(w, "list harness bindings failed", http.StatusBadGateway)
		return
	}
	profiles, err := s.harnessProfiles(r.Context())
	if err != nil {
		s.logf("list harness profiles", "err", err)
		http.Error(w, "list harness bindings failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, harnessView{Terminal: s.HarnessTerminal != nil, Bindings: bindings, Profiles: profiles})
}

// harnessBindings reads the credential-bindings document and each service's
// captured token state. A service with no captured set is not an error: it
// is exactly the state the setup terminal exists to change.
func (s *Server) harnessBindings(ctx context.Context, orgID string) ([]harnessBindingView, error) {
	response, err := s.ControlPlane.Query(ctx, &controlpb.QueryRequest{Kind: harnessBindingsKind})
	if err != nil {
		return nil, err
	}
	var docs []harnessBindingDoc
	if response.Resource != nil && len(response.Resource.ValueJson) > 0 {
		if err := json.Unmarshal(response.Resource.ValueJson, &docs); err != nil {
			return nil, err
		}
	}
	views := make([]harnessBindingView, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc.Service) == "" {
			continue
		}
		view := harnessBindingView{Service: doc.Service}
		secret, err := s.HarnessSecrets.GetHarnessSecret(ctx, orgID, doc.Service)
		switch {
		case err == nil:
			view.Captured = true
			if !secret.ExpiresAt.IsZero() {
				expires := secret.ExpiresAt
				view.ExpiresAt = &expires
			}
			if !secret.UpdatedAt.IsZero() {
				updated := secret.UpdatedAt
				view.UpdatedAt = &updated
			}
			view.Scopes = secret.Scopes
		case errors.Is(err, storecontract.ErrHarnessSecretNotFound):
			// Not captured yet: the page shows the binding as unconfigured.
		default:
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// harnessProfiles returns the agent profiles that name a Kit, sorted so the
// page's list does not depend on Go's map iteration order.
func (s *Server) harnessProfiles(ctx context.Context) ([]string, error) {
	response, err := s.ControlPlane.Query(ctx, &controlpb.QueryRequest{Kind: harnessProfilesKind})
	if err != nil {
		return nil, err
	}
	var docs map[string]harnessProfileDoc
	if response.Resource != nil && len(response.Resource.ValueJson) > 0 {
		if err := json.Unmarshal(response.Resource.ValueJson, &docs); err != nil {
			return nil, err
		}
	}
	profiles := make([]string, 0, len(docs))
	for name, doc := range docs {
		if strings.TrimSpace(doc.Kit) != "" {
			profiles = append(profiles, name)
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

// handleHarnessTerminal upgrades the request to a WebSocket and bridges it to
// a setup session opened for the named Kit profile. Opening the terminal is
// the update action on the secret, decided by the authorizer before this
// handler runs (authorize.go's route override).
func (s *Server) handleHarnessTerminal(w http.ResponseWriter, r *http.Request) {
	if s.HarnessTerminal == nil {
		http.Error(w, "setup terminal not configured", http.StatusServiceUnavailable)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profile == "" {
		http.Error(w, "profile is required", http.StatusBadRequest)
		return
	}
	session, err := s.HarnessTerminal.Open(r.Context(), string(org.OrgFromContext(r.Context())), profile)
	if err != nil {
		s.logf("open setup terminal", "err", err, "profile", profile)
		http.Error(w, "setup terminal failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = session.Close() }()
	websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		bridgeTerminal(ws, session)
	}).ServeHTTP(w, r)
}

// bridgeTerminal pumps bytes between the dashboard WebSocket and the setup
// session until either side closes, then closes both so the surviving copy
// unblocks. It is a free function so a test can drive it without a listener.
func bridgeTerminal(ws *websocket.Conn, session io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(session, ws); done <- struct{}{} }()
	go func() { _, _ = io.Copy(ws, session); done <- struct{}{} }()
	<-done
	_ = ws.Close()
	_ = session.Close()
	<-done
}
