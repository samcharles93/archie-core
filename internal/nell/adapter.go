// Package nell implements store.TaskStore on top of a NellDB log store.
//
// Tasks live in the "tasks" DocDB collection, keyed by
// "<owner>:<repo>:<issue_number>".  Events live in the "events" collection,
// keyed by an auto-incrementing counter.
//
// Secondary indexes are not available  --  ClaimNext scans all tasks, and
// lookups by task ID (Transition, Requeue, etc.) scan all tasks.  This is
// acceptable at the daemon's scale (tens to hundreds of tasks).
package nell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/samcharles93/NellDB"
	"github.com/samcharles93/NellDB/logstore"
	"github.com/samcharles93/NellDB/sdk"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// Adapter implements store.TaskStore backed by a NellDB store.
type Adapter struct {
	store  nell.Store // the underlying engine (LogStore or MemoryStore)
	tasks  *sdk.DocDB // "tasks" collection
	events *sdk.DocDB // "events" collection
	nodeID string
	mu     sync.Mutex // serialises ClaimNext, ClaimByIssue and atomic counter ops
}

// ── Compile-time check ───────────────────────────────────────────────────────

var _ store.TaskStore = (*Adapter)(nil)

// ── Open / lifecycle ─────────────────────────────────────────────────────────

// OpenStore opens a persistent NellDB-backed TaskStore at the given file path.
// Returns an error when path is empty.
func OpenStore(path, nodeID string) (store.TaskStore, error) {
	if path == "" {
		return nil, errors.New("nell: path is required")
	}
	st, err := logstore.OpenLog(path, nodeID)
	if err != nil {
		return nil, fmt.Errorf("nell: open log: %w", err)
	}
	return newAdapter(st, nodeID), nil
}

// OpenMemory creates an in-memory TaskStore backed by nell.MemoryStore,
// suitable for tests.
func OpenMemory(nodeID string) store.TaskStore {
	return newAdapter(nell.NewMemoryStore(nodeID), nodeID)
}

// Store returns the underlying NellDB engine this adapter wraps. Useful for
// creating additional SDK collections (e.g. sessions, messages) on the same
// persistent store without opening a second log.
func (a *Adapter) Store() nell.Store { return a.store }

func newAdapter(st nell.Store, nodeID string) *Adapter {
	return &Adapter{
		store:  st,
		tasks:  sdk.New(st, nodeID, "tasks"),
		events: sdk.New(st, nodeID, "events"),
		nodeID: nodeID,
	}
}

// Close shuts down the underlying store (flushes and closes the log file).
func (a *Adapter) Close() error {
	return a.store.Close()
}

// ── Task lifecycle ───────────────────────────────────────────────────────────

// EnqueueIssue inserts a new queued task; returns false if the issue is
// already tracked (the idempotency key is owner/repo/number). identity is
// the archie identity whose forge poll discovered this issue (empty for
// single-identity deployments).
func (a *Adapter) EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error) {
	key := taskKey(owner, repo, number)

	// Short-circuit if already tracked.
	existing, err := a.tasks.Get(ctx, key)
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return false, err
	}
	if err == nil {
		if status, _ := existing["status"].(string); status != "" {
			return false, nil
		}
		return false, nil
	}

	a.mu.Lock()
	taskID, err := a.nextTaskID(ctx)
	a.mu.Unlock()
	if err != nil {
		return false, err
	}

	doc := sdk.Doc{
		sdk.FieldID:        key,
		"id":               taskID,
		"owner":            owner,
		"repo":             repo,
		"issue_number":     int64(number),
		"title":            title,
		"body":             body,
		"labels":           labels,
		"status":           store.StatusQueued,
		"workflow":         "",
		"stage":            "",
		"branch":           "",
		"plan":             "",
		"notes":            "",
		"pr_number":        int64(0),
		"tokens_used":      int64(0),
		"iterations":       int64(0),
		"attempt":          int64(0),
		"park_reason":      "",
		"retry_count":      int64(0),
		"watch_comment_id": int64(0),
		"created_at":       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := a.tasks.Put(ctx, doc); err != nil {
		return false, fmt.Errorf("nell: enqueue: %w", err)
	}
	return true, nil
}

// EnqueueChatTask inserts a new queued task with source "chat" and no
// backing forge issue, returning the full created row with its real
// database ID. issueNumber is a synthetic value used only to satisfy
// the (owner, repo, issue_number) key uniqueness  --  callers must not
// treat it as a real forge issue number (see store.Task.IsForgeBacked).
func (a *Adapter) EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string, issueNumber int) (*store.Task, error) {
	key := taskKey(owner, repo, issueNumber)

	a.mu.Lock()
	taskID, err := a.nextTaskID(ctx)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}

	doc := sdk.Doc{
		sdk.FieldID:        key,
		"id":               taskID,
		"owner":            owner,
		"repo":             repo,
		"issue_number":     int64(issueNumber),
		"title":            title,
		"body":             body,
		"labels":           "chat",
		"status":           store.StatusQueued,
		"workflow":         workflow,
		"stage":            "",
		"branch":           "",
		"plan":             "",
		"notes":            "",
		"pr_number":        int64(0),
		"tokens_used":      int64(0),
		"iterations":       int64(0),
		"attempt":          int64(0),
		"park_reason":      "",
		"retry_count":      int64(0),
		"watch_comment_id": int64(0),
		"source":           store.SourceChat,
		"identity":         identity,
		"created_at":       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := a.tasks.Put(ctx, doc); err != nil {
		return nil, fmt.Errorf("nell: enqueue chat task: %w", err)
	}
	return docToTask(doc), nil
}

// TaskByID returns the task with the given database ID, or nil. Used by
// chat controls (/approve, /cancel) that reference a task by its real
// ID rather than a forge issue number.
func (a *Adapter) TaskByID(ctx context.Context, taskID int64) (*store.Task, error) {
	return a.findTaskByID(ctx, taskID)
}

// ClaimNext atomically moves the oldest queued task to running and returns
// it; returns nil when the queue is empty.
func (a *Adapter) ClaimNext(ctx context.Context) (*store.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}

	// Scan for the oldest queued task (lowest task ID).
	var best *store.Task
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		if t.Status != store.StatusQueued {
			continue
		}
		if best == nil || t.ID < best.ID {
			best = t
		}
	}
	if best == nil {
		return nil, nil
	}

	best.Status = store.StatusRunning
	best.Attempt++
	if err := a.putTask(ctx, taskKey(best.Owner, best.Repo, best.IssueNumber), best); err != nil {
		return nil, err
	}
	return best, nil
}

// ClaimByIssue atomically claims a queued task by owner/repo/issue_number.
// Returns nil if the task is not in queued state.
func (a *Adapter) ClaimByIssue(ctx context.Context, owner, repo string, number int) (*store.Task, error) {
	key := taskKey(owner, repo, number)

	a.mu.Lock()
	defer a.mu.Unlock()

	doc, err := a.tasks.Get(ctx, key)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	t := docToTask(doc)
	if t.Status != store.StatusQueued {
		return nil, nil
	}

	t.Status = store.StatusRunning
	t.Attempt++
	if err := a.putTask(ctx, key, t); err != nil {
		return nil, err
	}
	return t, nil
}

// TaskByIssue returns the task tracking an issue, or nil.
func (a *Adapter) TaskByIssue(ctx context.Context, owner, repo string, number int) (*store.Task, error) {
	doc, err := a.tasks.Get(ctx, taskKey(owner, repo, number))
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return docToTask(doc), nil
}

// Transition moves a task to the given status.
func (a *Adapter) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	t, err := a.findTaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("nell: task %d not found", taskID)
	}
	t.Status = to
	return a.putTaskByID(ctx, t)
}

// Update persists mutable task fields written by workflows.
// Only writes the subset of fields that workflows are allowed to mutate:
// workflow, stage, branch, plan, notes, pr_number, tokens_used,
// iterations, park_reason, watch_comment_id. Status and identity fields
// (owner, repo, issue_number, title, body, labels) are preserved.
func (a *Adapter) Update(ctx context.Context, t *store.Task) error {
	key := taskKey(t.Owner, t.Repo, t.IssueNumber)
	doc, err := a.tasks.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("nell: get task for update: %w", err)
	}
	// Copy only mutable fields  --  same set as the SQLite store's UPDATE.
	doc["workflow"] = t.Workflow
	doc["stage"] = t.Stage
	doc["branch"] = t.Branch
	doc["plan"] = t.Plan
	doc["notes"] = t.Notes
	doc["pr_number"] = int64(t.PRNumber)
	doc["tokens_used"] = int64(t.TokensUsed)
	doc["iterations"] = int64(t.Iterations)
	doc["park_reason"] = t.ParkReason
	doc["watch_comment_id"] = t.WatchCommentID
	if _, err := a.tasks.Put(ctx, doc); err != nil {
		return fmt.Errorf("nell: update task: %w", err)
	}
	return nil
}

// Requeue puts a task back on the queue with an optional workflow override.
func (a *Adapter) Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error {
	t, err := a.findTaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("nell: task %d not found", taskID)
	}
	t.Status = store.StatusQueued
	if workflow != "" {
		t.Workflow = workflow
	}
	t.Stage = ""
	t.ParkReason = ""
	return a.putTaskByID(ctx, t)
}

// RecoverStale re-queues all tasks left in "running" status.  Returns the
// number of tasks recovered.
func (a *Adapter) RecoverStale(ctx context.Context) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return 0, err
	}

	var count int64
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		if t.Status != store.StatusRunning {
			continue
		}
		t.Status = store.StatusQueued
		if err := a.putTask(ctx, row.ID, t); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// IncrementRetryCount bumps the retry_count on a task.
func (a *Adapter) IncrementRetryCount(ctx context.Context, taskID int64) error {
	t, err := a.findTaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("nell: task %d not found", taskID)
	}
	t.RetryCount++
	return a.putTaskByID(ctx, t)
}

// ── Queries ───────────────────────────────────────────────────────────────────

// WaitingTasks returns tasks in waiting_human state.
func (a *Adapter) WaitingTasks(ctx context.Context) ([]store.Task, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []store.Task
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		if t.Status != store.StatusWaitingHuman {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}

// OpenPRs returns tasks in pr_open state.
func (a *Adapter) OpenPRs(ctx context.Context) ([]store.Task, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []store.Task
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		if t.Status != store.StatusPROpen {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}

// ClearTerminalTasks removes tasks in terminal status.  Returns the count
// of removed tasks.
func (a *Adapter) ClearTerminalTasks(ctx context.Context) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return 0, err
	}
	var count int64
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		switch t.Status {
		case store.StatusMerged, store.StatusParked, store.StatusRejected, store.StatusClosedWontDo:
			if _, err := a.tasks.Remove(ctx, row.ID); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// ── Observability ────────────────────────────────────────────────────────────

// InsertEvent appends an event to the events collection and returns its ID.
func (a *Adapter) InsertEvent(ctx context.Context, e events.Event) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	id, err := a.nextEventID(ctx)
	if err != nil {
		return 0, err
	}

	dataJSON := "{}"
	if len(e.Data) > 0 {
		b, mbErr := json.Marshal(e.Data)
		if mbErr != nil {
			dataJSON = fmt.Sprintf(`{"marshal_error":%q}`, mbErr.Error())
		} else {
			dataJSON = string(b)
		}
	}

	doc := sdk.Doc{
		sdk.FieldID: fmt.Sprintf("event:%d", id),
		"id":        id,
		"at":        e.At.UTC().Format(time.RFC3339Nano),
		"kind":      e.Kind,
		"task_id":   e.TaskID,
		"repo":      e.Repo,
		"issue":     int64(e.Issue),
		"workflow":  e.Workflow,
		"stage":     e.Stage,
		"detail":    clip(e.Detail, 4000),
		"data":      dataJSON,
	}
	if _, err := a.events.Put(ctx, doc); err != nil {
		return 0, fmt.Errorf("nell: insert event: %w", err)
	}
	return id, nil
}

// EventsSince returns up to limit events with id > sinceID, oldest first.
func (a *Adapter) EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error) {
	all, err := a.events.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []events.Event
	for _, row := range all.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		e := docToEvent(row.Doc)
		if e.ID <= sinceID {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RecentEvents returns the newest limit events, newest first.
func (a *Adapter) RecentEvents(ctx context.Context, limit int) ([]events.Event, error) {
	all, err := a.events.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []events.Event
	for _, row := range all.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		e := docToEvent(row.Doc)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// TaskEvents returns a task's full event timeline, oldest first.
func (a *Adapter) TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error) {
	all, err := a.events.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []events.Event
	for _, row := range all.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		e := docToEvent(row.Doc)
		if e.TaskID != taskID {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Tasks returns all tasks, newest first (dashboard listing).
func (a *Adapter) Tasks(ctx context.Context, limit int) ([]store.Task, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []store.Task
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// StatusCounts returns task counts by status.
func (a *Adapter) StatusCounts(ctx context.Context) (map[string]int, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		counts[t.Status]++
	}
	return counts, nil
}

// WorkflowStats aggregates outcomes and spend per workflow.
func (a *Adapter) WorkflowStats(ctx context.Context) ([]store.WorkflowStat, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}

	type acc struct {
		Runs      int
		Merged    int
		PROpen    int
		Parked    int
		SumTokens int
		SumSteps  float64
	}
	agg := map[string]*acc{}

	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		if t.Workflow == "" {
			continue
		}
		a, ok := agg[t.Workflow]
		if !ok {
			a = &acc{}
			agg[t.Workflow] = a
		}
		a.Runs++
		a.SumTokens += t.TokensUsed
		a.SumSteps += float64(t.Iterations)
		switch t.Status {
		case store.StatusMerged:
			a.Merged++
		case store.StatusPROpen:
			a.PROpen++
		case store.StatusParked:
			a.Parked++
		}
	}

	// Deterministic ordering: most runs first, then by workflow name.
	keys := make([]string, 0, len(agg))
	for k := range agg {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if agg[keys[i]].Runs != agg[keys[j]].Runs {
			return agg[keys[i]].Runs > agg[keys[j]].Runs
		}
		return keys[i] < keys[j]
	})

	stats := make([]store.WorkflowStat, 0, len(keys))
	for _, wf := range keys {
		a := agg[wf]
		avgTokens := 0
		avgSteps := 0.0
		if a.Runs > 0 {
			avgTokens = a.SumTokens / a.Runs
			avgSteps = a.SumSteps / float64(a.Runs)
		}
		stats = append(stats, store.WorkflowStat{
			Workflow:   wf,
			Runs:       a.Runs,
			Merged:     a.Merged,
			PROpen:     a.PROpen,
			Parked:     a.Parked,
			AvgTokens:  avgTokens,
			AvgSteps:   avgSteps,
			TotalToken: a.SumTokens,
		})
	}
	return stats, nil
}

// StageStats aggregates stage_finish events  --  where time goes and which
// stages fail.
func (a *Adapter) StageStats(ctx context.Context) ([]store.StageStat, error) {
	all, err := a.events.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}

	type stageKey struct {
		workflow, stage string
	}
	type acc struct {
		Runs   int
		SumMs  int
		Errors int
	}
	agg := map[stageKey]*acc{}

	for _, row := range all.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		e := docToEvent(row.Doc)
		if e.Kind != events.KindStageFinish {
			continue
		}
		k := stageKey{workflow: e.Workflow, stage: e.Stage}
		a, ok := agg[k]
		if !ok {
			a = &acc{}
			agg[k] = a
		}
		a.Runs++
		if dur, _ := e.Data["duration_ms"].(float64); dur > 0 {
			a.SumMs += int(dur)
		}
		if errStr, _ := e.Data["error"].(string); errStr != "" {
			a.Errors++
		}
	}

	keys := make([]stageKey, 0, len(agg))
	for k := range agg {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].workflow != keys[j].workflow {
			return keys[i].workflow < keys[j].workflow
		}
		return keys[i].stage < keys[j].stage
	})

	stats := make([]store.StageStat, 0, len(keys))
	for _, k := range keys {
		a := agg[k]
		avgMs := 0
		if a.Runs > 0 {
			avgMs = a.SumMs / a.Runs
		}
		stats = append(stats, store.StageStat{
			Workflow: k.workflow,
			Stage:    k.stage,
			Runs:     a.Runs,
			AvgMs:    avgMs,
			Errors:   a.Errors,
		})
	}
	return stats, nil
}

// TokensByDay sums agent token spend per day from agent_finish events.
func (a *Adapter) TokensByDay(ctx context.Context, days int) ([]store.DayTokens, error) {
	all, err := a.events.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	agg := map[string]int{}
	var dayKeys []string

	for _, row := range all.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		e := docToEvent(row.Doc)
		if e.Kind != events.KindAgentFinish {
			continue
		}
		day := e.At.UTC().Format("2006-01-02")
		tokens := 0
		if t, ok := e.Data["tokens"].(float64); ok {
			tokens = int(t)
		}
		if _, seen := agg[day]; !seen {
			dayKeys = append(dayKeys, day)
		}
		agg[day] += tokens
	}

	sort.Slice(dayKeys, func(i, j int) bool { return dayKeys[i] > dayKeys[j] }) // newest first
	if days > 0 && len(dayKeys) > days {
		dayKeys = dayKeys[:days]
	}

	out := make([]store.DayTokens, len(dayKeys))
	for i, d := range dayKeys {
		out[i] = store.DayTokens{Day: d, Tokens: agg[d]}
	}
	return out, nil
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// taskKey builds the composite Doc ID for a task.
func taskKey(owner, repo string, number int) string {
	return fmt.Sprintf("%s:%s:%d", owner, repo, number)
}

// isMetaKey returns true for internal meta documents (e.g. counters).
func isMetaKey(key string) bool {
	return strings.HasPrefix(key, "meta:")
}

// docToTask reconstructs a *store.Task from a Doc.
func docToTask(doc sdk.Doc) *store.Task {
	return &store.Task{
		ID:             intField(doc, "id"),
		Owner:          strField(doc, "owner"),
		Repo:           strField(doc, "repo"),
		IssueNumber:    int(intField(doc, "issue_number")),
		Title:          strField(doc, "title"),
		Body:           strField(doc, "body"),
		Labels:         strField(doc, "labels"),
		Status:         strField(doc, "status"),
		Workflow:       strField(doc, "workflow"),
		Stage:          strField(doc, "stage"),
		Branch:         strField(doc, "branch"),
		Plan:           strField(doc, "plan"),
		Notes:          strField(doc, "notes"),
		PRNumber:       int(intField(doc, "pr_number")),
		TokensUsed:     int(intField(doc, "tokens_used")),
		Iterations:     int(intField(doc, "iterations")),
		Attempt:        int(intField(doc, "attempt")),
		ParkReason:     strField(doc, "park_reason"),
		RetryCount:     int(intField(doc, "retry_count")),
		WatchCommentID: intField(doc, "watch_comment_id"),
		Source:         sourceOrDefault(strField(doc, "source")),
		Identity:       strField(doc, "identity"),
	}
}

// sourceOrDefault returns store.SourceForge for docs written before the
// source field existed (empty string), preserving the "forge-backed
// unless explicitly chat-sourced" default the SQLite column default
// also encodes.
func sourceOrDefault(s string) string {
	if s == "" {
		return store.SourceForge
	}
	return s
}

// taskFields copies every mutable Task field into the Doc.  It does not
// touch _id or _rev so the caller can round-trip a live doc.
func taskFields(t *store.Task, doc sdk.Doc) {
	doc["id"] = t.ID
	doc["owner"] = t.Owner
	doc["repo"] = t.Repo
	doc["issue_number"] = int64(t.IssueNumber)
	doc["title"] = t.Title
	doc["body"] = t.Body
	doc["labels"] = t.Labels
	doc["status"] = t.Status
	doc["workflow"] = t.Workflow
	doc["stage"] = t.Stage
	doc["branch"] = t.Branch
	doc["plan"] = t.Plan
	doc["notes"] = t.Notes
	doc["pr_number"] = int64(t.PRNumber)
	doc["tokens_used"] = int64(t.TokensUsed)
	doc["iterations"] = int64(t.Iterations)
	doc["attempt"] = int64(t.Attempt)
	doc["park_reason"] = t.ParkReason
	doc["retry_count"] = int64(t.RetryCount)
	doc["watch_comment_id"] = t.WatchCommentID
}

// docToEvent reconstructs an events.Event from a Doc.
func docToEvent(doc sdk.Doc) events.Event {
	e := events.Event{
		ID:       intField(doc, "id"),
		Kind:     strField(doc, "kind"),
		TaskID:   intField(doc, "task_id"),
		Repo:     strField(doc, "repo"),
		Issue:    int(intField(doc, "issue")),
		Workflow: strField(doc, "workflow"),
		Stage:    strField(doc, "stage"),
		Detail:   strField(doc, "detail"),
	}
	if atStr := strField(doc, "at"); atStr != "" {
		e.At, _ = time.Parse(time.RFC3339Nano, atStr)
	}
	if dataStr := strField(doc, "data"); dataStr != "" {
		var data map[string]any
		if err := json.Unmarshal([]byte(dataStr), &data); err == nil {
			e.Data = data
		}
	}
	return e
}

// putTask performs a read-modify-write on a task document.  It first reads
// the current doc (to obtain the _rev for optimistic locking), stamps the
// mutable fields from *store.Task, then writes it back.
func (a *Adapter) putTask(ctx context.Context, key string, t *store.Task) error {
	doc, err := a.tasks.Get(ctx, key)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			doc = sdk.Doc{sdk.FieldID: key}
		} else {
			return err
		}
	}
	taskFields(t, doc)
	if _, err := a.tasks.Put(ctx, doc); err != nil {
		return fmt.Errorf("nell: put task: %w", err)
	}
	return nil
}

// putTaskByID is a convenience wrapper that computes the key from the task.
func (a *Adapter) putTaskByID(ctx context.Context, t *store.Task) error {
	return a.putTask(ctx, taskKey(t.Owner, t.Repo, t.IssueNumber), t)
}

// findTaskByID scans all tasks and returns the one with the given internal
// task ID.  Returns nil without error when the ID is not found.
func (a *Adapter) findTaskByID(ctx context.Context, taskID int64) (*store.Task, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		t := docToTask(row.Doc)
		if t.ID == taskID {
			return t, nil
		}
	}
	return nil, nil
}

// ── Auto-increment counters ─────────────────────────────────────────────────

// nextTaskID atomically reads and increments the task ID counter.
// Must be called under a.mu.
func (a *Adapter) nextTaskID(ctx context.Context) (int64, error) {
	return a.nextCounter(ctx, a.tasks, "meta:task_id_counter")
}

// nextEventID atomically reads and increments the event ID counter.
// Must be called under a.mu.
func (a *Adapter) nextEventID(ctx context.Context) (int64, error) {
	return a.nextCounter(ctx, a.events, "meta:event_id_counter")
}

// nextCounter implements the read-modify-write for a counter doc.
// Must be called under a.mu so that no two callers race on the _rev.
func (a *Adapter) nextCounter(ctx context.Context, db *sdk.DocDB, metaKey string) (int64, error) {
	doc, err := db.Get(ctx, metaKey)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			// First use: initialise to 2 (next call returns 1).
			doc = sdk.Doc{sdk.FieldID: metaKey, "next_id": int64(2)}
			if _, err := db.Put(ctx, doc); err != nil {
				return 0, err
			}
			return 1, nil
		}
		return 0, err
	}
	next := intField(doc, "next_id")
	doc["next_id"] = next + 1
	if _, err := db.Put(ctx, doc); err != nil {
		return 0, err
	}
	return next, nil
}

// ── Type-safe doc accessors ──────────────────────────────────────────────────

// strField extracts a string from a Doc by key.  Returns "" when missing.
func strField(doc sdk.Doc, key string) string {
	s, _ := doc[key].(string)
	return s
}

// intField extracts an int64 from a Doc by key.  JSON numbers round-trip as
// float64 through the SDK's Doc map, so both float64 and int64 are handled.
func intField(doc sdk.Doc, key string) int64 {
	switch v := doc[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// clip truncates a string to at most n bytes, preserving full runes.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	pos := 0
	for pos < len(s) {
		_, size := utf8.DecodeRuneInString(s[pos:])
		if pos+size > n {
			break
		}
		pos += size
	}
	return s[:pos]
}
