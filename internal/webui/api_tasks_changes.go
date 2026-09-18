package webui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// taskChangesView is GET /api/tasks/{id}/changes: the change captures recorded
// for one attempt. Found reports that a capture was recorded at all -- an empty
// captures list without it would tell an operator that nothing changed, which is
// not a claim this system can make about a run whose diffstat was never read.
type taskChangesView struct {
	TaskID   int64               `json:"task_id"`
	Attempt  int                 `json:"attempt"`
	Found    bool                `json:"found"`
	Captures []changeCaptureView `json:"captures"`
}

// changeCaptureView is one capture: the diffstat measured at the moment the run
// was committed or pushed, plus the repository and pull-request links the page
// renders beside it. The URLs are filled from the same forge projection the task
// list uses, so a capture links exactly where its task does.
type changeCaptureView struct {
	CapturedAt    time.Time         `json:"captured_at"`
	CapturedAfter string            `json:"captured_after"`
	Stage         string            `json:"stage"`
	Owner         string            `json:"owner"`
	Repo          string            `json:"repo"`
	Base          string            `json:"base"`
	Branch        string            `json:"branch"`
	HeadSHA       string            `json:"head_sha"`
	BaseSHA       string            `json:"base_sha"`
	PRNumber      int               `json:"pr_number"`
	RepoURL       string            `json:"repo_url,omitempty"`
	PRURL         string            `json:"pr_url,omitempty"`
	Totals        task.ChangeTotals `json:"totals"`
	Truncated     bool              `json:"truncated"`
	Files         []task.FileChange `json:"files"`
}

// capturePayload is one changes_captured event's data as the producer persists
// it. The measured change itself reuses the shared task.ChangeStats identity the
// producer writes with, so the two sides cannot drift into two spellings of one
// diffstat.
type capturePayload struct {
	Schema        string `json:"schema"`
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	Base          string `json:"base"`
	Branch        string `json:"branch"`
	PRNumber      int    `json:"pr_number"`
	CapturedAfter string `json:"captured_after"`
	Truncated     bool   `json:"truncated"`
	task.ChangeStats
}

// capturePayloadKeys is the exact key set one capture carries, derived from
// capturePayload's own json tags rather than restated beside them: the reader
// and the key set cannot disagree. A payload that renamed, dropped or added a
// key is refused, because reading it leniently is how a lost truncation marker
// becomes "not truncated" and a dropped file list becomes "nothing changed".
var capturePayloadKeys = structKeys(reflect.TypeFor[capturePayload]())

// structKeys collects the JSON key names a struct declares, recursing into the
// embedded value structs a decoder flattens.
func structKeys(t reflect.Type) map[string]struct{} {
	keys := make(map[string]struct{}, t.NumField())
	for field := range t.Fields() {
		if field.Anonymous {
			for key := range structKeys(field.Type) {
				keys[key] = struct{}{}
			}
			continue
		}
		if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name != "" && name != "-" {
			keys[name] = struct{}{}
		}
	}
	return keys
}

// handleTaskChanges serves the changed files one attempt produced.
func (s *Server) handleTaskChanges(w http.ResponseWriter, r *http.Request) {
	t, attempt, ok := s.taskAttemptTarget(w, r)
	if !ok {
		return
	}
	evs, err := s.Store.TaskEvents(r.Context(), t.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	captures, recorded := capturedChanges(evs, attempt)

	// The links come from the task's own forge resolution, reused rather than
	// re-derived: the capture's own owner/repo/pr decide the URL, so a capture
	// taken before the PR existed has no pull-request link instead of one
	// pointing at a later attempt's PR.
	forge := s.resolveForge(r.Context())
	for i := range captures {
		target := task.Task{
			Owner: captures[i].Owner, Repo: captures[i].Repo,
			PRNumber: captures[i].PRNumber, Identity: t.Identity,
		}
		captures[i].RepoURL, _, captures[i].PRURL = taskURLs(target, forge(target))
	}

	writeJSON(w, taskChangesView{TaskID: t.ID, Attempt: attempt, Found: recorded, Captures: captures})
}

// capturedChanges folds a task's events into the captures recorded for one
// attempt, oldest first (events are read in insert order, which is capture
// order).
//
// recorded reports that the attempt has at least one capture event even when its
// payload could not be read: a capture that happened is provenance whether or not
// this read can decode it, and answering "no capture was recorded" would be a
// false statement about it. A payload that is absent, in a schema this build does
// not read, or otherwise malformed is skipped rather than failing the read.
func capturedChanges(evs []events.Event, attempt int) (captures []changeCaptureView, recorded bool) {
	captures = []changeCaptureView{}
	for _, e := range evs {
		if e.Kind != events.KindChangesCaptured || e.Attempt != attempt {
			continue
		}
		recorded = true
		capture, ok := decodeCapture(e)
		if !ok {
			continue
		}
		captures = append(captures, capture)
	}
	return captures, recorded
}

// decodeCapture reads one capture event. It reports false when the event carries
// no data, when its payload is not in the schema this build reads, when its key
// set is not exactly the one this reader declares, or when the payload is
// malformed. The totals are passed through as recorded and never recomputed from
// the file entries, because the producer's totals cover the full change set even
// when the entries were capped.
func decodeCapture(e events.Event) (changeCaptureView, bool) {
	if len(e.Data) == 0 {
		return changeCaptureView{}, false
	}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return changeCaptureView{}, false
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return changeCaptureView{}, false
	}
	if len(keys) != len(capturePayloadKeys) {
		return changeCaptureView{}, false
	}
	for key := range capturePayloadKeys {
		if _, ok := keys[key]; !ok {
			return changeCaptureView{}, false
		}
	}
	var payload capturePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return changeCaptureView{}, false
	}
	if payload.Schema != events.ChangesCapturedSchema {
		return changeCaptureView{}, false
	}
	files := payload.Files
	if len(files) > task.MaxCapturedFiles {
		// The producer bounds the list at task.MaxCapturedFiles and reports the
		// drop with truncated. More entries than the shared cap means the cap
		// was lost, and rendering them would present the loss as a complete
		// capture.
		return changeCaptureView{}, false
	}
	if files == nil {
		files = []task.FileChange{}
	}
	return changeCaptureView{
		CapturedAt:    e.At,
		CapturedAfter: payload.CapturedAfter,
		Stage:         e.Stage,
		Owner:         payload.Owner,
		Repo:          payload.Repo,
		Base:          payload.Base,
		Branch:        payload.Branch,
		HeadSHA:       payload.HeadSHA,
		BaseSHA:       payload.BaseSHA,
		PRNumber:      payload.PRNumber,
		Totals:        payload.Totals,
		Truncated:     payload.Truncated,
		Files:         files,
	}, true
}
