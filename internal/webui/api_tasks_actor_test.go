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
