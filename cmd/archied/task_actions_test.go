package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// The dashboard and chat both let an operator approve or decline a task, and
// they had drifted: declining from the dashboard left the task
// closed_wont_do, cancelling the same task from chat left it rejected. Two
// operators looking at one queue saw different states for the same decision,
// and "rejected" -- which reconcilePRs uses for a pull request closed without
// merging -- no longer meant one thing.
//
// This drives both real surfaces against one store and asserts they agree.
// It lives in cmd/archied because that is the only place both are wired.
func TestDashboardAndChatAgreeOnTerminalStates(t *testing.T) {
	tests := []struct {
		name string
		// viaChat runs the action through the chat router; otherwise it
		// goes through the dashboard's HTTP handler.
		viaChat bool
		action  string
		want    string
	}{
		{name: "dashboard reject", action: "reject", want: store.StatusClosedWontDo},
		{name: "chat cancel", viaChat: true, action: "cancel", want: store.StatusClosedWontDo},
		{name: "dashboard approve", action: "approve", want: store.StatusQueued},
		{name: "chat approve", viaChat: true, action: "approve", want: store.StatusQueued},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })

			task, err := st.EnqueueChatTask(ctx, "acme", "widget", "decide on me", "", "", "reviewer")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Transition(ctx, task.ID, store.StatusQueued, store.StatusWaitingHuman, "await"); err != nil {
				t.Fatal(err)
			}

			if tc.viaChat {
				controller := gateway.NewStoreTaskController(chatTaskControllerAdapter{
					taskByID:   st.TaskByID,
					requeue:    st.Requeue,
					transition: st.Transition,
				})
				var actErr error
				if tc.action == "approve" {
					actErr = controller.Approve(ctx, task.ID, "reviewer")
				} else {
					actErr = controller.Cancel(ctx, task.ID, "reviewer")
				}
				if actErr != nil {
					t.Fatalf("chat %s: %v", tc.action, actErr)
				}
			} else {
				srv := &webui.Server{Store: st, Cfg: config.NewHolder(config.Config{MaxRetries: 3})}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action",
					bytes.NewBufferString(`{"action":"`+tc.action+`"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Archie-CSRF", "1")
				w := httptest.NewRecorder()
				srv.Handler().ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("dashboard %s: status %d, body %s", tc.action, w.Code, w.Body)
				}
			}

			got, err := st.TaskByID(ctx, task.ID)
			if err != nil || got == nil {
				t.Fatalf("TaskByID = (%+v, %v)", got, err)
			}
			if got.Status != tc.want {
				t.Errorf("status = %q, want %q: the two surfaces disagree about "+
					"what this action means", got.Status, tc.want)
			}
		})
	}
}
