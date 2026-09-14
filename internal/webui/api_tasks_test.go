package webui

// Tests for the task-timeline route. The dashboard's "Click a row for its
// timeline" interaction depends on GET /api/tasks/{id} returning exactly that
// task's events, ordered, and on the literal /api/tasks/clear route beating
// the {id} wildcard.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
)

// A process with no configuration of its own still has to render the links,
// or the extracted dashboard loses every click-through to the issue and the
// pull request it opened. The daemon's published projection is where they
// come from once the holder is gone.
func TestTaskURLsFallBackToThePublishedProjection(t *testing.T) {
	tests := []struct {
		name    string
		view    ConfigView
		ok      bool
		err     error
		wantPR  string
		wantURL string
	}{
		{
			name:    "published projection",
			view:    ConfigView{Identity: IdentityView{ForgeType: "github", ForgeHost: "https://github.example"}},
			ok:      true,
			wantURL: "https://github.example/acme/widget",
			wantPR:  "https://github.example/acme/widget/pull/34",
		},
		{
			// A document published before the deployment carried per-identity
			// forges says so and carries none, so there is nothing to
			// attribute this task to. Withholding beats guessing.
			name: "a multi-identity document with no per-identity forges is withheld",
			view: ConfigView{
				Identity:      IdentityView{ForgeType: "github", ForgeHost: "https://github.example"},
				MultiIdentity: true,
			},
			ok: true,
		},
		{
			name: "no snapshot published yet",
		},
		{
			name: "snapshot read failed",
			err:  errors.New("state store down"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{ConfigSource: func(context.Context) (ConfigView, bool, error) {
				return tc.view, tc.ok, tc.err
			}}
			task := workflow.Task{Owner: "acme", Repo: "widget", IssueNumber: 12, PRNumber: 34}
			forge := srv.resolveForge(t.Context())
			repoURL, _, prURL := taskURLs(task, forge(task))
			if repoURL != tc.wantURL || prURL != tc.wantPR {
				t.Fatalf("repo/PR URLs = %q, %q; want %q, %q", repoURL, prURL, tc.wantURL, tc.wantPR)
			}
		})
	}
}

// multiIdentityView is a two-forge deployment as the daemon publishes it: the
// default identity on GitHub, and a Gitea identity owning one repository.
func multiIdentityView() ConfigView {
	return ConfigView{
		Identity:      IdentityView{ForgeType: "github", ForgeHost: "https://github.example"},
		MultiIdentity: true,
		Identities: []ForgeIdentityView{
			{
				Name: "gitea-bot", ForgeType: "gitea", ForgeHost: "https://gitea.example",
				Repos: []ForgeRepoView{{Owner: "acme", Name: "widget"}, {Owner: "acme", Name: "shared"}},
			},
			{
				Name: "github-bot", ForgeType: "github", ForgeHost: "https://github.example",
				Repos: []ForgeRepoView{{Owner: "beta", Name: "svc"}, {Owner: "acme", Name: "shared"}},
			},
		},
	}
}

// TestResolveForgeMatchesEachTaskToItsOwningIdentity ports the matching rules
// recovered from 57d9be5's forgeConfigForTask onto the published projection:
// exact identity name first, then repository ownership, with an ambiguous
// match falling back to the default forge. Without them every row in a
// multi-identity deployment renders no link at all.
func TestResolveForgeMatchesEachTaskToItsOwningIdentity(t *testing.T) {
	srv := &Server{ConfigSource: func(context.Context) (ConfigView, bool, error) {
		return multiIdentityView(), true, nil
	}}
	forge := srv.resolveForge(t.Context())

	tests := []struct {
		name       string
		task       workflow.Task
		wantHost   string
		wantPRPath string
	}{
		{
			// The name is authoritative even though the identity that owns
			// this repo would otherwise claim the task.
			name:       "exact identity name wins over repo ownership",
			task:       workflow.Task{Identity: "gitea-bot", Owner: "acme", Repo: "shared", PRNumber: 34},
			wantHost:   "https://gitea.example",
			wantPRPath: "/pulls/34",
		},
		{
			name:       "repo ownership resolves a task with no identity recorded",
			task:       workflow.Task{Owner: "beta", Repo: "svc", PRNumber: 7},
			wantHost:   "https://github.example",
			wantPRPath: "/pull/7",
		},
		{
			name:       "an identity the projection does not carry falls back to repo ownership",
			task:       workflow.Task{Identity: "retired", Owner: "acme", Repo: "widget", PRNumber: 12},
			wantHost:   "https://gitea.example",
			wantPRPath: "/pulls/12",
		},
		{
			// Two identities list acme/shared, so ownership cannot decide.
			// Pointing at either would be a wrong link; the default forge is
			// the same answer the pre-cutover resolver gave.
			name:       "an ambiguous repo belongs to the default forge",
			task:       workflow.Task{Owner: "acme", Repo: "shared", PRNumber: 3},
			wantHost:   "https://github.example",
			wantPRPath: "/pull/3",
		},
		{
			name:       "no identity owns the repo",
			task:       workflow.Task{Owner: "other", Repo: "repo", PRNumber: 9},
			wantHost:   "https://github.example",
			wantPRPath: "/pull/9",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coords := forge(tc.task)
			if coords.host != tc.wantHost {
				t.Fatalf("host = %q, want %q", coords.host, tc.wantHost)
			}
			repoURL, issueURL, prURL := taskURLs(tc.task, coords)
			wantRepo := tc.wantHost + "/" + tc.task.Owner + "/" + tc.task.Repo
			if repoURL != wantRepo {
				t.Errorf("repo URL = %q, want %q", repoURL, wantRepo)
			}
			if tc.task.IssueNumber > 0 && issueURL == "" {
				t.Errorf("issue URL is empty for forge-backed task with issue number %d", tc.task.IssueNumber)
			}
			if prURL != wantRepo+tc.wantPRPath {
				t.Errorf("PR URL = %q, want %q", prURL, wantRepo+tc.wantPRPath)
			}
		})
	}
}

// TestResolveForgeWithoutIdentitiesUsesTheDefault: a single-identity
// deployment publishes no identity list, so every task belongs to the one
// forge the projection carries -- exactly what this resolver did before
// per-identity forges were published.
func TestResolveForgeWithoutIdentitiesUsesTheDefault(t *testing.T) {
	srv := &Server{ConfigSource: func(context.Context) (ConfigView, bool, error) {
		return ConfigView{Identity: IdentityView{ForgeType: "github", ForgeHost: "https://github.example"}}, true, nil
	}}
	forge := srv.resolveForge(t.Context())

	for _, task := range []workflow.Task{
		{Owner: "acme", Repo: "widget"},
		{Identity: "whatever", Owner: "acme", Repo: "widget"},
	} {
		if coords := forge(task); coords.host != "https://github.example" {
			t.Errorf("task %+v resolved to forge %q, want the deployment's only forge", task, coords.host)
		}
	}
}

// TestHandleTasksLinksEachRowToItsOwningForge proves the fix at the route the
// dashboard actually reads: a multi-identity deployment's task list carries a
// forge link on every row, built from the forge that owns that row.
func TestHandleTasksLinksEachRowToItsOwningForge(t *testing.T) {
	base := newTestServer(t)
	ctx := t.Context()
	if _, err := base.Store.EnqueueIssue(ctx, "acme", "widget", 12, "gitea work", "", "", "gitea-bot"); err != nil {
		t.Fatal(err)
	}
	if _, err := base.Store.EnqueueIssue(ctx, "beta", "svc", 7, "github work", "", "", "github-bot"); err != nil {
		t.Fatal(err)
	}
	tasks, err := base.Store.Tasks(ctx, 10)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("seeded tasks = %d (%v), want 2", len(tasks), err)
	}
	for i := range tasks {
		tasks[i].PRNumber = tasks[i].IssueNumber
		if err := base.Store.Update(ctx, &tasks[i]); err != nil {
			t.Fatal(err)
		}
	}

	srv := &Server{
		Store: base.Store,
		Log:   base.Log,
		ConfigSource: func(context.Context) (ConfigView, bool, error) {
			return multiIdentityView(), true, nil
		},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/tasks = %d (%s), want 200", w.Code, w.Body)
	}
	var rows []taskView
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode rows: %v (%s)", err, w.Body)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (%s)", len(rows), w.Body)
	}

	want := map[string]struct{ repo, issue, pr string }{
		"acme/widget": {"https://gitea.example/acme/widget", "https://gitea.example/acme/widget/issues/12", "https://gitea.example/acme/widget/pulls/12"},
		"beta/svc":    {"https://github.example/beta/svc", "https://github.example/beta/svc/issues/7", "https://github.example/beta/svc/pull/7"},
	}
	for _, row := range rows {
		key := row.Owner + "/" + row.Repo
		expected, ok := want[key]
		if !ok {
			t.Errorf("unexpected row %q", key)
			continue
		}
		if row.RepoURL != expected.repo || row.IssueURL != expected.issue || row.PRURL != expected.pr {
			t.Errorf("%s links = %q, %q, %q; want %q, %q, %q", key,
				row.RepoURL, row.IssueURL, row.PRURL, expected.repo, expected.issue, expected.pr)
		}
	}
}

// Approve requeues from the task's own recorded state rather than anything
// the caller supplies, so a stale dashboard cannot approve a task twice.
func TestHandleTaskActionApproveUsesRecordedState(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "approve me", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%+v, %v)", task, err)
	}
	if err := srv.Store.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusWaitingHuman, "review"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action", bytes.NewBufferString(`{"action":"approve"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body = %s", w.Code, w.Body)
	}
	got, err := srv.Store.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusQueued || got.Workflow != "implement" {
		t.Fatalf("approved task = %+v, want queued/implement", got)
	}
}

func TestHandleTaskActionRetriesParkedTask(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "retry me", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%+v, %v)", task, err)
	}
	if err := srv.Store.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusParked, "failed"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action", bytes.NewBufferString(`{"action":"retry"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", w.Code, w.Body)
	}
	got, err := srv.Store.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusQueued || got.RetryCount != 1 {
		t.Fatalf("retried task = %+v, want queued/retry_count=1", got)
	}
}

// TestHandleTaskIsolatesTasks returns the timeline for one task, and only
// that task: another task's events must not leak into the response.
func TestHandleTaskIsolatesTasks(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	for _, ev := range []events.Event{
		{TaskID: 7, Kind: "stage_start", Stage: "plan", Detail: "bootstrap"},
		{TaskID: 7, Kind: "stage_end", Stage: "plan"},
		{TaskID: 8, Kind: "stage_start", Stage: "implement"},
	} {
		if _, err := srv.Store.InsertEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks/7", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var got []events.Event
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("timeline has %d events, want 2 (task 8 must not leak in)", len(got))
	}
	if got[0].Kind != "stage_start" || got[0].Stage != "plan" {
		t.Errorf("first event = %+v, want stage_start/plan", got[0])
	}
}

// TestHandleTaskUnknownID returns an empty list, not an error, for a task id
// that has no events yet -- the UI renders "No events yet" from that.
func TestHandleTaskUnknownID(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/999", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "null" && body != "[]" {
		t.Fatalf("body = %s, want an empty timeline", body)
	}
}

// TestHandleTaskBadID is covered in webui_test.go (400 on a non-numeric id).

// TestHandleTaskStoreError proves a store failure surfaces as 500 -- the UI
// shows its "Could not load timeline" state and offers a retry.
func TestHandleTaskStoreError(t *testing.T) {
	base := newTestServer(t)
	srv := &Server{
		Store: &stubStore{TaskStore: base.Store, taskEventsErr: fmt.Errorf("db locked")},
		Log:   base.Log,
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/7", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestBulkClearRouteCannotMutateTasks(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/clear", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("GET /api/tasks/clear still exposes a destructive mutation")
	}
	tasks, err := srv.Store.Tasks(t.Context(), 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after legacy clear request = (%d, %v), want one preserved", len(tasks), err)
	}
}
