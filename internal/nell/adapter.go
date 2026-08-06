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

	nell "github.com/samcharles93/NellDB"
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
	a, err := openAdapter(st, nodeID)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("nell: migrate counters: %w", err)
	}
	return a, nil
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

func openAdapter(st nell.Store, nodeID string) (*Adapter, error) {
	a := newAdapter(st, nodeID)
	// Carry forward a pre-rename ID sequence before anything can allocate.
	if err := a.migrateCounters(context.Background()); err != nil {
		return nil, err
	}
	return a, nil
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
		// The identity whose poll found this issue. Dropping it here (as this
		// did) left every forge-originated task unattributed, so multi-identity
		// dispatch and any per-identity listing saw an empty queue. The SQLite
		// store has always persisted it; this keeps the two in step.
		"identity":   identity,
		"created_at": nowRFC3339(),
		"updated_at": nowRFC3339(),
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
		"created_at":       nowRFC3339(),
		"updated_at":       nowRFC3339(),
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
//
// from is a guard, not decoration: the write only happens when the task is
// still in that status, and otherwise store.ErrStaleTransition is returned.
// This adapter accepted the argument and ignored it, so every compare-and-swap
// the callers rely on was absent on the backend that actually ships -- eight
// concurrent dashboard retries requeued one task four times, and a
// waiting_human task the daemon had since claimed could be yanked back to
// queued and executed twice.
//
// The mutex is what makes the check meaningful. A document store has no
// conditional update, so read-then-write has to be serialised; the daemon is
// a single process, which is where the concurrency actually is.
func (a *Adapter) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	t, err := a.findTaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("nell: task %d not found", taskID)
	}
	if from != "" && t.Status != from {
		return store.ErrStaleTransition
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
	doc["updated_at"] = nowRFC3339()
	if _, err := a.tasks.Put(ctx, doc); err != nil {
		return fmt.Errorf("nell: update task: %w", err)
	}
	return nil
}

// Requeue puts a task back on the queue with an optional workflow override.
//
// fromStatus guards the write, matching Transition and the SQLite store: a
// task that has moved on since the caller read it is not requeued, and
// store.ErrStaleTransition says so.
func (a *Adapter) Requeue(ctx context.Context, taskID int64, fromStatus, workflow string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	t, err := a.findTaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("nell: task %d not found", taskID)
	}
	if fromStatus != "" && t.Status != fromStatus {
		return store.ErrStaleTransition
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
// IncrementRetryCount bumps retry_count by one.
//
// Serialised: as a bare read-modify-write, twenty concurrent calls landed a
// count of one, so the retry cap could be evaded indefinitely by holding the
// dashboard's Retry button down.
func (a *Adapter) IncrementRetryCount(ctx context.Context, taskID int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

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

// OpenPRs returns tasks in pr_open state.
func (a *Adapter) OpenPRs(ctx context.Context) ([]store.Task, error) {
	result, err := a.tasks.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []store.Task
	for _, row := range result.Rows {
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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

// TaskEvents returns a task's full event timeline, oldest first.
func (a *Adapter) TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error) {
	all, err := a.events.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, err
	}
	var out []events.Event
	for _, row := range all.Rows {
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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
		if row.Doc == nil || isReservedKey(row.ID) {
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

// isReservedKey returns true for bookkeeping documents that share a collection
// with real records and must be skipped by every scan.
//
// The counter documents are matched by exact id, not by prefix. Task keys are
// "<owner>:<repo>:<number>", so a prefix rule would also match every task in a
// repository owned by "counter" -- EnqueueIssue would report success and the
// task would then be invisible to ClaimNext, Tasks and StatusCounts, never run
// and never be re-enqueued. Exact matching costs nothing and removes that whole
// class.
//
// "meta:" stays a prefix match: it covers the pre-rename counters plus the
// SDK's own meta:clock / meta:vector records, which do surface in AllDocs and
// would otherwise be parsed as tasks. It carries the same owner-named-"meta"
// caveat, which predates this change.
func isReservedKey(key string) bool {
	switch key {
	case taskCounterID, eventCounterID:
		return true
	}
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
		CreatedAt:      timeField(doc, "created_at"),
		UpdatedAt:      timeField(doc, "updated_at"),
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
	doc["updated_at"] = nowRFC3339()
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
		if row.Doc == nil || isReservedKey(row.ID) {
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

// Counter document keys.
//
// These deliberately do NOT use a "meta:" prefix. The SDK reserves that prefix
// for its own bookkeeping (meta:clock, meta:vector) and matches it by prefix,
// not by exact name: sdk.isInternalID is `id[:5] == "meta:"`. Two consequences
// followed from squatting on it, and both were live defects:
//
//   - DocDB.reindex skips internal ids when rebuilding the in-memory rev cache
//     on open, while the record itself stays in the log. After a restart, Get
//     returned the counter complete with its _rev but the cache had no entry,
//     and putIn rejects exactly that pairing with ErrConflict. Every task
//     enqueue and every event insert failed, permanently, for the life of the
//     database.
//   - isInternalID exists to keep SDK bookkeeping out of replication, so the
//     counters were silently excluded from it. Two instances sharing a log
//     would have allocated colliding task IDs.
//
// Renaming fixes both. Do not move these back under "meta:".
//
// Renaming does NOT make allocation multi-writer safe. The increment is a
// read-modify-write serialised only by Adapter.mu, which is process-local: two
// adapters or disconnected replicas can still allocate the same id, and an
// event write to the same "event:<id>" key would then be resolved by LWW,
// silently dropping one. Nothing in archie wires an sdk.Replicator today, so
// each log must have exactly one writing adapter; a replicated deployment
// needs a different, distributed-atomic allocation scheme.
const (
	counterPrefix  = "counter:"
	taskCounterID  = counterPrefix + "task_id"
	eventCounterID = counterPrefix + "event_id"

	// Legacy keys, read once by migrateCounters and never written again.
	legacyTaskCounterID  = "meta:task_id_counter"
	legacyEventCounterID = "meta:event_id_counter"
)

// migrateCounters seeds the counters from their pre-rename documents.
//
// The old documents are readable -- only writing them fails -- and the value
// in them is load-bearing: starting a fresh sequence at 1 would hand out IDs
// that already belong to live tasks and events. The old document is left in
// place because removing it is the operation that cannot succeed; scans skip
// it via isReservedKey.
//
// Runs at open, before any caller can allocate. A missing legacy counter is
// normal for a fresh install; any other read or write failure aborts opening
// the store so allocation cannot silently restart at one.
func (a *Adapter) migrateCounters(ctx context.Context) error {
	migrate := func(db *sdk.DocDB, current, legacy string) error {
		doc, err := db.Get(ctx, current)
		if err == nil {
			if next := intField(doc, "next_id"); next < 1 {
				return fmt.Errorf("current counter %q has invalid next_id %d", current, next)
			}
			return nil // already migrated
		}
		if !errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("read current counter %q: %w", current, err)
		}
		old, err := db.Get(ctx, legacy)
		if errors.Is(err, sdk.ErrNotFound) {
			return nil // fresh database, nothing to carry over
		}
		if err != nil {
			return fmt.Errorf("read legacy counter %q: %w", legacy, err)
		}
		next := intField(old, "next_id")
		if next < 1 {
			return fmt.Errorf("legacy counter %q has invalid next_id %d", legacy, next)
		}
		// A new document, so no _rev is carried across from the old one.
		if _, err := db.Put(ctx, sdk.Doc{sdk.FieldID: current, "next_id": next}); err != nil {
			return fmt.Errorf("write current counter %q: %w", current, err)
		}
		return nil
	}
	if err := migrate(a.tasks, taskCounterID, legacyTaskCounterID); err != nil {
		return err
	}
	return migrate(a.events, eventCounterID, legacyEventCounterID)
}

// nextTaskID atomically reads and increments the task ID counter.
// Must be called under a.mu.
func (a *Adapter) nextTaskID(ctx context.Context) (int64, error) {
	doc, err := a.tasks.Get(ctx, taskCounterID)
	if errors.Is(err, sdk.ErrNotFound) {
		// First use: initialise to 2 so the first allocated ID is 1.
		if _, err := a.tasks.Put(ctx, sdk.Doc{sdk.FieldID: taskCounterID, "next_id": int64(2)}); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	next := intField(doc, "next_id")
	if next < 1 {
		return 0, fmt.Errorf("nell: counter %q has invalid next_id %d", taskCounterID, next)
	}
	doc["next_id"] = next + 1
	if _, err := a.tasks.Put(ctx, doc); err != nil {
		return 0, err
	}
	return next, nil
}

// nextEventID atomically reads and increments the event ID counter.
// Must be called under a.mu.
func (a *Adapter) nextEventID(ctx context.Context) (int64, error) {
	doc, err := a.events.Get(ctx, eventCounterID)
	if errors.Is(err, sdk.ErrNotFound) {
		// First use: initialise to 2 so the first allocated ID is 1.
		if _, err := a.events.Put(ctx, sdk.Doc{sdk.FieldID: eventCounterID, "next_id": int64(2)}); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	next := intField(doc, "next_id")
	if next < 1 {
		return 0, fmt.Errorf("nell: counter %q has invalid next_id %d", eventCounterID, next)
	}
	doc["next_id"] = next + 1
	if _, err := a.events.Put(ctx, doc); err != nil {
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

// nowRFC3339 is the timestamp format task documents use. Kept in one place so
// created_at and updated_at cannot drift apart in format.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// timeField parses an RFC3339 document field, returning the zero time when it
// is absent or unparseable. Documents written before these fields existed
// simply report a zero timestamp rather than failing the read.
func timeField(doc sdk.Doc, key string) time.Time {
	v := strField(doc, key)
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
