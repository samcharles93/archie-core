package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
)

// postActionWithCredential posts an operator action the way an authenticated
// caller does: JSON, the CSRF header, and the bearer token the middleware
// resolves.
func postActionWithCredential(t *testing.T, srv *Server, id int64, action, credential string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/tasks/"+strconv.FormatInt(id, 10)+"/action",
		bytes.NewBufferString(fmt.Sprintf(`{"action":%q}`, action)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	req.Header.Set("Authorization", "Bearer "+credential)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// approvalEvent returns the recorded approval on a task's timeline.
func approvalEvent(t *testing.T, srv *Server, taskID int64) events.Event {
	t.Helper()
	recorded, err := srv.Store.TaskEvents(t.Context(), taskID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	for _, event := range recorded {
		if event.Kind == events.KindHumanApproved || event.Kind == events.KindAgentApproved || event.Kind == events.KindTaskApproved {
			return event
		}
	}
	t.Fatalf("no approval was recorded on task %d (events: %+v)", taskID, recorded)
	return events.Event{}
}

// TestApprovalRecordsTheVerifiedActor is the flip this whole boundary exists for:
// a dashboard approval is recorded as the person whose credential verified, and an
// agent's token is recorded as the agent's action -- not as task_approved for both,
// and never as a human's.
func TestApprovalRecordsTheVerifiedActor(t *testing.T) {
	tests := []struct {
		name      string
		identity  identity.Identity
		wantKind  string
		wantActor string
	}{
		{
			name:      "a person's credential is recorded as that person",
			identity:  identity.Identity{ID: "50000000-0000-5000-8000-000000000001", Kind: identity.KindUser, DisplayName: "sam", Lifecycle: identity.LifecycleActive},
			wantKind:  events.KindHumanApproved,
			wantActor: "50000000-0000-5000-8000-000000000001",
		},
		{
			name:      "an agent's credential is recorded as the agent",
			identity:  identity.Identity{ID: "50000000-0000-5000-8000-000000000002", Kind: identity.KindBot, DisplayName: "archie-web", Lifecycle: identity.LifecycleActive},
			wantKind:  events.KindAgentApproved,
			wantActor: "50000000-0000-5000-8000-000000000002",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, task, _, _, _ := actionServer(t, workflow.StatusWaitingHuman, "needs a decision")
			// The middleware resolves a verified credential to this identity, which
			// is the only place the actor may come from.
			srv.Authenticate = func(context.Context, string) (identity.Identity, error) {
				return tc.identity, nil
			}

			// The request also claims an identity of its own, which must be ignored.
			w := postActionWithCredential(t, srv, task.ID, "approve", "a-provider-token")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, http.StatusOK, w.Body.String())
			}

			recorded := approvalEvent(t, srv, task.ID)
			if recorded.ActorID != tc.wantActor {
				t.Fatalf("actor = %q, want %q", recorded.ActorID, tc.wantActor)
			}
			if recorded.ActorKind != string(tc.identity.Kind) {
				t.Fatalf("actor kind = %q, want %q", recorded.ActorKind, tc.identity.Kind)
			}
			if recorded.Kind != tc.wantKind {
				t.Fatalf("event kind = %q, want %q", recorded.Kind, tc.wantKind)
			}
		})
	}
}

// TestUnverifiedApprovalIsRecordedUnattributed: with no provider configured the
// shared-token gate still admits a request, and that request carries no identity
// -- so the action is recorded unattributed rather than credited to a human.
func TestUnverifiedApprovalIsRecordedUnattributed(t *testing.T) {
	srv, task, _, _, _ := actionServer(t, workflow.StatusWaitingHuman, "needs a decision")

	w := postAction(t, srv, task.ID, "approve")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", w.Code, http.StatusOK, w.Body.String())
	}

	recorded := approvalEvent(t, srv, task.ID)
	if recorded.ActorID != "" || recorded.ActorKind != "" || recorded.PrincipalID != "" {
		t.Fatalf("an unattributed approval recorded an actor: %+v", recorded)
	}
	if recorded.Kind != events.KindTaskApproved {
		t.Fatalf("event kind = %q, want %q", recorded.Kind, events.KindTaskApproved)
	}
}

// TestActionRequestCannotChooseItsActor: the body is read for the action only.
// Nothing a caller can put in the request may select the identity recorded.
func TestActionRequestCannotChooseItsActor(t *testing.T) {
	srv, task, _, _, _ := actionServer(t, workflow.StatusWaitingHuman, "needs a decision")
	verified := identity.Identity{ID: "50000000-0000-5000-8000-000000000003", Kind: identity.KindUser, DisplayName: "sam", Lifecycle: identity.LifecycleActive}
	srv.Authenticate = func(context.Context, string) (identity.Identity, error) { return verified, nil }

	body := map[string]any{"action": "approve", "actor": "forged", "actor_id": "forged", "identity": "forged"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	req.Header.Set("Authorization", "Bearer a-provider-token")
	req.Header.Set("X-Archie-Actor", "forged")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// The body carries fields the decoder rejects, which is the point: there is no
	// accepted way to name an actor, so the request is refused rather than
	// silently attributed.
	if w.Code == http.StatusOK {
		recorded := approvalEvent(t, srv, task.ID)
		if recorded.ActorID != string(verified.ID) {
			t.Fatalf("actor = %q, want the verified %q", recorded.ActorID, verified.ID)
		}
		return
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 or 200 with the verified actor", w.Code)
	}
}

// stubFlow stands in for the provider's authorization-code flow. The flow itself
// is exercised against a real provider in internal/infrastructure/oidc; what is
// proved here is that the dashboard's browser path reaches the recorded actor.
type stubFlow struct {
	authURL  string
	verifier string
	session  identity.ProviderSession
}

func (f stubFlow) AuthCodeURL(state string) (string, string) {
	return f.authURL + "?state=" + state, f.verifier
}

func (f stubFlow) Exchange(context.Context, string, string) (identity.ProviderSession, error) {
	return f.session, nil
}

func cookieNamed(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range response.Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no %q cookie in the response", name)
	return nil
}

// TestBrowserSignInRecordsTheSignedInPerson drives the whole browser path: sign in
// through the provider, come back with a code, and then act on a task holding only
// the credential the flow stored -- the actor recorded must be that person.
func TestBrowserSignInRecordsTheSignedInPerson(t *testing.T) {
	srv, task, _, _, _ := actionServer(t, workflow.StatusWaitingHuman, "needs a decision")
	person := identity.Identity{
		ID: "60000000-0000-5000-8000-000000000001", Kind: identity.KindUser,
		DisplayName: "sam", Lifecycle: identity.LifecycleActive,
	}
	const token = "provider-access-token"
	srv.Login = stubFlow{
		authURL:  "http://provider.example/authorize",
		verifier: "pkce-verifier",
		session: identity.ProviderSession{
			Credential: identity.Credential{Subject: identity.Subject{Issuer: "https://idp.example", Subject: "sam"}},
			Token:      token,
		},
	}
	srv.Authenticate = func(_ context.Context, raw string) (identity.Identity, error) {
		if raw != token {
			return identity.Identity{}, identity.ErrCredentialRejected
		}
		return person, nil
	}

	// 1. A browser asks to sign in and is sent to the provider.
	loginRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth2/login", nil)
	loginResponse := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", loginResponse.Code, http.StatusSeeOther)
	}
	state := cookieNamed(t, loginResponse.Result(), loginStateCookie)
	verifier := cookieNamed(t, loginResponse.Result(), loginVerifierCookie)

	// 2. The provider sends the browser back with a code.
	callbackRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/oauth2/callback?code=a-code&state="+state.Value, nil)
	callbackRequest.AddCookie(state)
	callbackRequest.AddCookie(verifier)
	callbackResponse := httptest.NewRecorder()
	srv.Handler().ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want %d (body %q)", callbackResponse.Code, http.StatusSeeOther, callbackResponse.Body.String())
	}

	// 3. The signed-in browser acts, presenting only what the flow stored.
	stored := cookieNamed(t, callbackResponse.Result(), providerTokenCookie)
	actionRequest := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action", bytes.NewBufferString(`{"action":"approve"}`))
	actionRequest.Header.Set("Content-Type", "application/json")
	actionRequest.Header.Set("X-Archie-CSRF", "1")
	actionRequest.AddCookie(stored)
	actionResponse := httptest.NewRecorder()
	srv.Handler().ServeHTTP(actionResponse, actionRequest)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("action status = %d, want %d (body %q)", actionResponse.Code, http.StatusOK, actionResponse.Body.String())
	}

	recorded := approvalEvent(t, srv, task.ID)
	if recorded.ActorID != string(person.ID) {
		t.Fatalf("actor = %q, want the signed-in person %q", recorded.ActorID, person.ID)
	}
	if recorded.Kind != events.KindHumanApproved {
		t.Fatalf("event kind = %q, want %q", recorded.Kind, events.KindHumanApproved)
	}
}

// TestCallbackRefusesAMismatchedState: the callback must check the state it issued,
// or a sign-in response could be replayed against another browser's flow.
func TestCallbackRefusesAMismatchedState(t *testing.T) {
	srv, _, _, _, _ := actionServer(t, workflow.StatusWaitingHuman, "needs a decision")
	srv.Login = stubFlow{authURL: "http://provider.example/authorize", verifier: "pkce-verifier"}
	srv.Authenticate = func(context.Context, string) (identity.Identity, error) {
		t.Fatal("the callback verified a credential before checking its state")
		return identity.Identity{}, nil
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/oauth2/callback?code=a-code&state=not-the-state-i-issued", nil)
	request.AddCookie(&http.Cookie{Name: loginStateCookie, Value: "the-state-i-issued"})
	request.AddCookie(&http.Cookie{Name: loginVerifierCookie, Value: "pkce-verifier"})
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
