package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// captureData is the shape the producer persists for one capture: the diffstat
// measured at the moment a run was committed or pushed. The reader's fixtures
// are built here; nothing in this file re-derives a capture.
func captureData(files []any, totals map[string]any) map[string]any {
	return map[string]any{
		"schema": events.ChangesCapturedSchema,
		"owner":  "acme", "repo": "widget", "base": "main", "branch": "feat/2-x",
		"head_sha": "head-sha", "base_sha": "base-sha", "pr_number": 0,
		"captured_after": "commit-push",
		"files":          files, "totals": totals, "truncated": false,
	}
}

func modifiedFile(path string, additions, deletions int) map[string]any {
	return map[string]any{
		"path": path, "old_path": "", "status": "modified",
		"additions": additions, "deletions": deletions, "binary": false,
	}
}

func insertCapture(t *testing.T, srv *Server, taskID int64, attempt int, stage string, data map[string]any) {
	t.Helper()
	insertTaskEvent(t, srv, events.Event{
		Kind: events.KindChangesCaptured, TaskID: taskID, Attempt: attempt, Stage: stage,
		At: railBase.Add(time.Duration(attempt) * time.Minute), Data: data,
	})
}

// changesResponse is the endpoint's body as the page consumes it, decoded by
// field name so the contract is asserted rather than substring-matched.
type changesResponse struct {
	TaskID   int64             `json:"task_id"`
	Attempt  int               `json:"attempt"`
	Found    bool              `json:"found"`
	Captures []captureViewJSON `json:"captures"`
}

type captureViewJSON struct {
	CapturedAt    time.Time        `json:"captured_at"`
	CapturedAfter string           `json:"captured_after"`
	Stage         string           `json:"stage"`
	Owner         string           `json:"owner"`
	Repo          string           `json:"repo"`
	Base          string           `json:"base"`
	Branch        string           `json:"branch"`
	HeadSHA       string           `json:"head_sha"`
	BaseSHA       string           `json:"base_sha"`
	PRNumber      int              `json:"pr_number"`
	RepoURL       string           `json:"repo_url"`
	PRURL         string           `json:"pr_url"`
	Totals        changeTotalsJSON `json:"totals"`
	Truncated     bool             `json:"truncated"`
	Files         []fileChangeJSON `json:"files"`
}

type changeTotalsJSON struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type fileChangeJSON struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

func decodeChanges(t *testing.T, body []byte) changesResponse {
	t.Helper()
	var decoded changesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	return decoded
}

// TestHandleTaskChangesKeepsFoundFalseApartFromAnEmptyFileList pins the
// distinction the changed-files view depends on: "no capture was recorded for
// this attempt" is not "no files changed".
func TestHandleTaskChangesKeepsFoundFalseApartFromAnEmptyFileList(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)
	insertCapture(t, srv, current.ID, 2, "commit",
		captureData([]any{}, map[string]any{"files": 0, "additions": 0, "deletions": 0}))

	recorded := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
	if recorded.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorded.Code, recorded.Body)
	}
	if raw := recorded.Body.String(); !strings.Contains(raw, `"files":[]`) {
		t.Errorf("body = %s, want an empty files array rather than null", raw)
	}
	body := decodeChanges(t, recorded.Body.Bytes())
	if !body.Found || body.Attempt != 2 {
		t.Fatalf("found = %v, attempt = %d; want true, 2", body.Found, body.Attempt)
	}
	if len(body.Captures) != 1 {
		t.Fatalf("captures = %d, want 1 (%s)", len(body.Captures), recorded.Body)
	}
	if len(body.Captures[0].Files) != 0 {
		t.Errorf("files = %+v, want none: a capture with an empty diff is still a capture", body.Captures[0].Files)
	}
	if body.Captures[0].Totals.Files != 0 {
		t.Errorf("totals.files = %d, want 0", body.Captures[0].Totals.Files)
	}

	unrecorded := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=1"))
	if unrecorded.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", unrecorded.Code, unrecorded.Body)
	}
	if raw := unrecorded.Body.String(); !strings.Contains(raw, `"captures":[]`) {
		t.Errorf("body = %s, want an empty captures array rather than null", raw)
	}
	if got := decodeChanges(t, unrecorded.Body.Bytes()); got.Found || len(got.Captures) != 0 || got.Attempt != 1 {
		t.Errorf("attempt 1 = found %v with %d captures at attempt %d, want found false, none, attempt 1",
			got.Found, len(got.Captures), got.Attempt)
	}
}

// TestHandleTaskChangesResolvesTheCurrentAttemptAndCarriesTheRecordedFields
// pins the wire shape field by field, and that an absent attempt parameter
// selects the task's current attempt.
func TestHandleTaskChangesResolvesTheCurrentAttemptAndCarriesTheRecordedFields(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)
	data := captureData([]any{modifiedFile("internal/x.go", 12, 3)},
		map[string]any{"files": 1, "additions": 12, "deletions": 3})
	data["pr_number"] = 7
	insertCapture(t, srv, current.ID, 2, "commit-push", data)

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	body := decodeChanges(t, w.Body.Bytes())
	if body.TaskID != current.ID || body.Attempt != 2 || !body.Found {
		t.Fatalf("envelope = task %d attempt %d found %v; want %d, 2, true",
			body.TaskID, body.Attempt, body.Found, current.ID)
	}
	if len(body.Captures) != 1 {
		t.Fatalf("captures = %d, want 1 (%s)", len(body.Captures), w.Body)
	}
	capture := body.Captures[0]
	if !capture.CapturedAt.Equal(railBase.Add(2 * time.Minute)) {
		t.Errorf("captured_at = %v, want the capture event's own time", capture.CapturedAt)
	}
	if capture.CapturedAfter != "commit-push" || capture.Stage != "commit-push" {
		t.Errorf("captured_after = %q, stage = %q; want commit-push, commit-push", capture.CapturedAfter, capture.Stage)
	}
	if capture.Owner != "acme" || capture.Repo != "widget" || capture.Base != "main" || capture.Branch != "feat/2-x" {
		t.Errorf("repository fields = %q/%q base %q branch %q", capture.Owner, capture.Repo, capture.Base, capture.Branch)
	}
	if capture.HeadSHA != "head-sha" || capture.BaseSHA != "base-sha" || capture.PRNumber != 7 {
		t.Errorf("shas = %q/%q pr %d, want head-sha/base-sha/7", capture.HeadSHA, capture.BaseSHA, capture.PRNumber)
	}
	if capture.Totals.Files != 1 || capture.Totals.Additions != 12 || capture.Totals.Deletions != 3 {
		t.Errorf("totals = %+v, want 1/12/3", capture.Totals)
	}
	if capture.Truncated {
		t.Error("truncated = true for a capture that was not truncated")
	}
	if len(capture.Files) != 1 {
		t.Fatalf("files = %+v, want one", capture.Files)
	}
	file := capture.Files[0]
	if file.Path != "internal/x.go" || file.Status != "modified" || file.Additions != 12 || file.Deletions != 3 || file.Binary {
		t.Errorf("file = %+v, want internal/x.go modified +12 -3, not binary", file)
	}
	if file.OldPath != "" {
		t.Errorf("old_path = %q, want empty for a modify", file.OldPath)
	}
}

// TestHandleTaskChangesKeepsTheFullTotalsOfATruncatedCapture pins that totals
// are the producer's measurement of the whole change set, not a re-count of the
// file entries this read happens to carry.
func TestHandleTaskChangesKeepsTheFullTotalsOfATruncatedCapture(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)
	data := captureData([]any{modifiedFile("internal/x.go", 1, 1)},
		map[string]any{"files": 250, "additions": 900, "deletions": 10})
	data["truncated"] = true
	insertCapture(t, srv, current.ID, 2, "commit", data)

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	captures := decodeChanges(t, w.Body.Bytes()).Captures
	if len(captures) != 1 {
		t.Fatalf("captures = %d, want 1 (%s)", len(captures), w.Body)
	}
	capture := captures[0]
	if !capture.Truncated {
		t.Error("truncated = false for a capture that dropped file entries")
	}
	if len(capture.Files) != 1 {
		t.Fatalf("files = %d, want the single entry the producer capped the list to", len(capture.Files))
	}
	if capture.Totals.Files != 250 || capture.Totals.Additions != 900 || capture.Totals.Deletions != 10 {
		t.Errorf("totals = %+v, want the full 250/900/10 rather than a count of the 1 file carried", capture.Totals)
	}
}

// TestHandleTaskChangesRefusesACaptureItCannotNameSchema pins the schema
// marker's job: a payload whose shape this build does not know is not a
// capture it may render. Rendering it would show a diffstat nobody read, so
// the attempt reports that a capture was recorded and none was readable --
// the panel's read-failure state -- rather than a capture of zeroes.
func TestHandleTaskChangesRefusesACaptureItCannotNameSchema(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name:   "no schema at all",
			mutate: func(data map[string]any) { delete(data, "schema") },
		},
		{
			name:   "a schema this build does not know",
			mutate: func(data map[string]any) { data["schema"] = "archie/task-changes@2" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			current := twoAttemptTask(t, srv)
			data := captureData([]any{modifiedFile("internal/x.go", 12, 3)},
				map[string]any{"files": 1, "additions": 12, "deletions": 3})
			tt.mutate(data)
			insertCapture(t, srv, current.ID, 2, "commit", data)

			w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", w.Code, w.Body)
			}
			body := decodeChanges(t, w.Body.Bytes())
			if !body.Found {
				t.Error("found = false although a capture event was recorded for the attempt")
			}
			if len(body.Captures) != 0 {
				t.Errorf("captures = %+v, want none: a payload this build cannot name must not be rendered as a capture",
					body.Captures)
			}
		})
	}
}

// TestHandleTaskChangesRefusesAPayloadWhoseKeySetDrifted pins the other half
// of the schema guard: the schema names the shape, the exact key set proves
// the shape arrived. A key renamed, dropped or added means the payload is not
// the one this reader knows, and reading it leniently is how a lost
// truncation marker becomes "not truncated" and a moved cap becomes "every
// file, none dropped".
func TestHandleTaskChangesRefusesAPayloadWhoseKeySetDrifted(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "the truncation marker renamed",
			mutate: func(data map[string]any) {
				delete(data, "truncated")
				data["truncated_files"] = true
			},
		},
		{
			name:   "the file list dropped",
			mutate: func(data map[string]any) { delete(data, "files") },
		},
		{
			name:   "a key this reader does not know",
			mutate: func(data map[string]any) { data["schema_version"] = 1 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t)
			current := twoAttemptTask(t, srv)
			data := captureData([]any{modifiedFile("internal/x.go", 1, 0)},
				map[string]any{"files": task.MaxCapturedFiles + 1, "additions": 1, "deletions": 0})
			tt.mutate(data)
			insertCapture(t, srv, current.ID, 2, "commit", data)

			w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", w.Code, w.Body)
			}
			body := decodeChanges(t, w.Body.Bytes())
			if !body.Found {
				t.Error("found = false although a capture event was recorded for the attempt")
			}
			if len(body.Captures) != 0 {
				t.Errorf("captures = %+v, want none: a payload whose keys drifted must not be read as zero values",
					body.Captures)
			}
		})
	}
}

// TestHandleTaskChangesLinksThePullRequestOfTheFinalCapture is the reader half
// of the requirement the commit-time captures cannot meet: their pr_number is
// necessarily 0, and the capture OpenPR takes once the number exists is what
// gives the panel a pull-request link.
func TestHandleTaskChangesLinksThePullRequestOfTheFinalCapture(t *testing.T) {
	srv := newTestServer(t)
	srv.ConfigSource = func(_ context.Context) (ConfigView, bool, error) {
		return multiIdentityView(), true, nil
	}
	current := twoAttemptTask(t, srv)

	beforePR := captureData([]any{modifiedFile("internal/x.go", 1, 0)},
		map[string]any{"files": 1, "additions": 1, "deletions": 0})
	beforePR["captured_after"] = "commit-push"
	insertCapture(t, srv, current.ID, 2, "commit-push", beforePR)

	withPR := captureData([]any{modifiedFile("internal/x.go", 1, 0)},
		map[string]any{"files": 1, "additions": 1, "deletions": 0})
	withPR["captured_after"] = "open-pr"
	withPR["pr_number"] = 7
	insertCapture(t, srv, current.ID, 2, "open-pr", withPR)

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	captures := decodeChanges(t, w.Body.Bytes()).Captures
	if len(captures) != 2 {
		t.Fatalf("captures = %d, want both the push and the open-pr capture (%s)", len(captures), w.Body)
	}
	if captures[0].PRNumber != 0 || captures[0].PRURL != "" {
		t.Errorf("the push capture = pr %d url %q, want no PR: it was taken before one existed",
			captures[0].PRNumber, captures[0].PRURL)
	}
	if captures[1].CapturedAfter != "open-pr" || captures[1].PRNumber != 7 {
		t.Fatalf("the final capture = %q pr %d, want open-pr, 7", captures[1].CapturedAfter, captures[1].PRNumber)
	}
	if captures[1].PRURL != "https://gitea.example/acme/widget/pulls/7" {
		t.Errorf("pr_url = %q, want the pull request the capture names", captures[1].PRURL)
	}
}

// TestHandleTaskChangesRefusesACaptureThatLostItsFileCap pins the cap from the
// reading side: `files` is bounded at task.MaxCapturedFiles by the producer,
// and a payload carrying more than that is one whose cap was lost. Rendering
// it would advertise the loss as a complete capture of every file.
func TestHandleTaskChangesRefusesACaptureThatLostItsFileCap(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)
	files := make([]any, 0, task.MaxCapturedFiles+1)
	for i := range task.MaxCapturedFiles + 1 {
		files = append(files, modifiedFile(fmt.Sprintf("file-%03d.go", i), 1, 0))
	}
	insertCapture(t, srv, current.ID, 2, "commit",
		captureData(files, map[string]any{"files": len(files), "additions": len(files), "deletions": 0}))

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	body := decodeChanges(t, w.Body.Bytes())
	if !body.Found {
		t.Error("found = false although a capture event was recorded for the attempt")
	}
	if len(body.Captures) != 0 {
		t.Errorf("captures = %d entries, want none: a payload over the cap is one whose cap was lost",
			len(body.Captures))
	}
}

// TestHandleTaskChangesSkipsCapturesWithNoReadablePayload pins the degrade
// rule: a capture whose data is absent or malformed is skipped rather than
// failing the read, and the attempt still reports that a capture was recorded.
func TestHandleTaskChangesSkipsCapturesWithNoReadablePayload(t *testing.T) {
	srv := newTestServer(t)
	current := twoAttemptTask(t, srv)

	insertTaskEvent(t, srv, events.Event{
		Kind: events.KindChangesCaptured, TaskID: current.ID, Attempt: 2, Stage: "commit",
		At: railBase, Data: map[string]any{"files": "not-an-array"},
	})
	insertTaskEvent(t, srv, events.Event{
		Kind: events.KindChangesCaptured, TaskID: current.ID, Attempt: 2, Stage: "commit",
		At: railBase.Add(time.Second),
	})
	insertCapture(t, srv, current.ID, 2, "commit",
		captureData([]any{modifiedFile("internal/kept.go", 1, 0)},
			map[string]any{"files": 1, "additions": 1, "deletions": 0}))

	w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	body := decodeChanges(t, w.Body.Bytes())
	if !body.Found {
		t.Error("found = false although a capture event was recorded for the attempt")
	}
	if len(body.Captures) != 1 {
		t.Fatalf("captures = %+v, want only the readable one", body.Captures)
	}
	if got := body.Captures[0].Files[0].Path; got != "internal/kept.go" {
		t.Errorf("kept file = %q, want internal/kept.go", got)
	}
}

// TestHandleTaskChangesLinksTheCaptureRepositoryAndPullRequest reuses the task
// list's own forge projection: the page needs a repository link and, once a PR
// exists, a pull-request link, and it must get them from the same source the
// task list uses rather than a second one.
func TestHandleTaskChangesLinksTheCaptureRepositoryAndPullRequest(t *testing.T) {
	srv := newTestServer(t)
	srv.ConfigSource = func(_ context.Context) (ConfigView, bool, error) {
		return multiIdentityView(), true, nil
	}
	current := twoAttemptTask(t, srv)

	for _, prNumber := range []int{0, 7} {
		data := captureData([]any{modifiedFile("internal/x.go", 1, 0)},
			map[string]any{"files": 1, "additions": 1, "deletions": 0})
		data["pr_number"] = prNumber
		insertCapture(t, srv, current.ID, 2, "commit", data)

		w := getTaskAPI(t, srv, taskRoute(current.ID, "/changes?attempt=2"))
		if w.Code != http.StatusOK {
			t.Fatalf("pr %d: status = %d, body %s", prNumber, w.Code, w.Body)
		}
		captures := decodeChanges(t, w.Body.Bytes()).Captures
		capture := captures[len(captures)-1]
		if capture.RepoURL != "https://gitea.example/acme/widget" {
			t.Errorf("pr %d: repo_url = %q, want the task's gitea repository", prNumber, capture.RepoURL)
		}
		wantPR := ""
		if prNumber > 0 {
			wantPR = "https://gitea.example/acme/widget/pulls/7"
		}
		if capture.PRURL != wantPR {
			t.Errorf("pr %d: pr_url = %q, want %q", prNumber, capture.PRURL, wantPR)
		}
	}
}
