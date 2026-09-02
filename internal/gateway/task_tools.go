package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/tools"
)

const (
	// defaultTaskListLimit is how many tasks a bare task_list returns.
	defaultTaskListLimit = 20

	// maxTaskListLimit bounds what the model can ask for. A queue of a few
	// hundred tasks would otherwise land in the context window whole.
	maxTaskListLimit = 100

	// defaultTaskLogLimit is how many log entries a bare task_logs returns.
	defaultTaskLogLimit = 200

	// maxTaskLogLimit bounds what the model can ask for in one call.
	maxTaskLogLimit = 2000
)

// ChatTaskSummary is the per-task view the task tools return. It is
// deliberately narrower than a store task: notes, plan and body are the bulk
// of a row and none of them help answer "what are you working on?".
type ChatTaskSummary struct {
	ID       int64  `json:"id"`
	Repo     string `json:"repo"` // "owner/name"
	Title    string `json:"title"`
	Status   string `json:"status"`
	Workflow string `json:"workflow,omitempty"`
	Stage    string `json:"stage,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
	// Attempt is the task's current retry attempt. 0 or 1 means it has never
	// been retried; the /tasks command only surfaces this once it is
	// actionable information (attempt > 1).
	Attempt int `json:"attempt,omitempty"`
	// ParkReason explains why a parked task is parked. Empty for any other
	// status.
	ParkReason string `json:"park_reason,omitempty"`
	// UpdatedAt is the task's last transition time, so a caller can tell a
	// task that is running but stuck (old UpdatedAt) from one making
	// progress (recent UpdatedAt).
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// ChatTaskLister is the read surface the task tools need. It mirrors the
// existing chatTaskWriter/chatTaskController pattern: gateway states what it
// needs and the daemon supplies an adapter over the store, so this package
// keeps its independence from internal/store.
type ChatTaskLister interface {
	ListChatTasks(ctx context.Context, identity string, limit int) ([]ChatTaskSummary, error)
}

// TaskListResult is what task_list returns.
type TaskListResult struct {
	Tasks []ChatTaskSummary `json:"tasks"`
}

// TaskSpawnResult is what task_spawn returns.
type TaskSpawnResult struct {
	ID      int64  `json:"id"`
	Message string `json:"message"`
}

// TaskActionResult is what task_action returns.
type TaskActionResult struct {
	TaskID  int64  `json:"task_id"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

// ChatTaskActor is the mutation surface task_action needs. It mirrors the
// ChatTaskLister pattern: gateway states what it needs and the daemon
// supplies an adapter over the store and runtime, so this package keeps its
// independence from internal/store.
type ChatTaskActor interface {
	// ApplyChatTaskAction executes action on taskID, scoped to identity's own tasks.
	ApplyChatTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action) (TaskActionResult, error)
}

// ChatTaskLogEntry is one log line as task_logs returns it. It mirrors
// internal/logging.Entry's shape without importing that package: gateway
// keeps its independence from internal/logging the same way it already does
// from internal/store, with the daemon adapting between them.
type ChatTaskLogEntry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// ChatTaskLogQuery filters a task_logs read.
type ChatTaskLogQuery struct {
	// Component matches the log entry's component field case-insensitively.
	// Empty means any.
	Component string
	// Contains matches the message or any field value, case-insensitively.
	Contains string
	// Levels restricts results to these levels (case-insensitive). Empty
	// means all levels.
	Levels []string
	// Since excludes entries whose timestamp is strictly before this. Zero
	// time disables the bound.
	Since time.Time
	// Until excludes entries whose timestamp is strictly after this. Zero
	// time disables the bound.
	Until time.Time
	// AfterID is the byte-offset cursor returned in the previous page's
	// Cursor field. Zero means "start at the beginning of the scanned
	// window". The model treats this as opaque and just passes it back
	// unchanged; the byte offset is not intended to be human-readable.
	AfterID int64
	// Limit caps returned entries.
	Limit int
}

// ChatTaskLogResult is what task_logs returns.
type ChatTaskLogResult struct {
	Entries []ChatTaskLogEntry `json:"entries"`
	// Attempt is the attempt number actually read, so the model can tell
	// the caller which run it looked at when none was requested explicitly.
	Attempt int `json:"attempt"`
	// Truncated reports that older matching entries exist beyond what this
	// page examined (the on-disk scan hit its size cap before EOF).
	Truncated bool `json:"truncated"`
	// Cursor is the opaque byte offset to pass back as AfterID to read the
	// next page. It is the file offset just past the last line this page
	// returned, so a non-zero value after a full page means "resume here";
	// when MoreAvailable is false the caller is done regardless of the
	// value. Zero means the log was missing or empty, not "exhausted".
	Cursor int64 `json:"cursor"`
	// MoreAvailable is true when the scan saw more matching entries that
	// did not fit in this page. Combine with Truncated: a result can be
	// MoreAvailable=false while still Truncated=true when the scan ended
	// at the size cap, or MoreAvailable=true while Truncated=false when
	// more matches remain within the same scan window.
	MoreAvailable bool `json:"more_available"`
}

// ChatTaskLogReader is the read surface task_logs needs. It mirrors the
// ChatTaskLister pattern: gateway states what it needs and the daemon
// supplies an adapter, so this package keeps its independence from
// internal/store and internal/logging.
type ChatTaskLogReader interface {
	// ReadChatTaskLogs returns taskID's log entries for attempt (0 selects
	// the task's current/latest attempt), scoped to identity's own tasks.
	ReadChatTaskLogs(ctx context.Context, identity string, taskID int64, attempt int, q ChatTaskLogQuery) (ChatTaskLogResult, error)
}

// TaskTools builds the chat tools that let Archie see, add to, and manage its own work
// queue. Until these existed, /spawn and the task list were slash commands only
// a human could type, so "what are you working on?" had no tool path at all.
//
// All tools are bound to identity at construction. The model never supplies
// it: a model that could name an identity could read, mutate or file work
// belonging to another instance on the same host.
//
// A nil backend omits its tool rather than registering one that always fails,
// so a daemon without chat task support advertises nothing.
func TaskTools(lister ChatTaskLister, creator TaskCreator, logs ChatTaskLogReader, actor ChatTaskActor, identity string) []tools.ToolEntry {
	var entries []tools.ToolEntry
	if lister != nil {
		entries = append(entries, taskListTool(lister, identity))
	}
	if creator != nil {
		entries = append(entries, taskSpawnTool(creator, identity))
	}
	if logs != nil {
		entries = append(entries, taskLogsTool(logs, identity))
	}
	if actor != nil {
		entries = append(entries, taskActionTool(actor, identity))
	}
	return entries
}

func taskListTool(lister ChatTaskLister, identity string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "task_list",
		Toolset: "tasks",
		Description: "List the tasks this instance is working on, most recent first. " +
			"Use it to answer questions about current, queued, parked or finished work. " +
			"Optionally filter by status (queued, running, waiting_human, parked, pr_open, merged, rejected).",
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "Only return tasks with this status. Omit for all statuses.",
				},
				"limit": map[string]any{
					"type": "integer",
					"description": fmt.Sprintf(
						"Maximum tasks to return. Defaults to %d, capped at %d.",
						defaultTaskListLimit, maxTaskListLimit,
					),
				},
			},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			limit := taskListLimit(input)

			// The bound identity, never input["identity"].
			all, err := lister.ListChatTasks(ctx, identity, limit)
			if err != nil {
				return nil, fmt.Errorf("task_list: %w", err)
			}

			status := strings.TrimSpace(strings.ToLower(asString(input["status"])))
			// Non-nil even when empty: an empty queue is an answer, and a
			// null would read to the model as a failed call worth retrying.
			filtered := make([]ChatTaskSummary, 0, len(all))
			for _, task := range all {
				if status != "" && !strings.EqualFold(task.Status, status) {
					continue
				}
				filtered = append(filtered, task)
			}
			return TaskListResult{Tasks: filtered}, nil
		},
	}
}

func taskSpawnTool(creator TaskCreator, identity string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "task_spawn",
		Toolset: "tasks",
		Description: "Queue a new task for this instance to work on in its own worktree, " +
			"opening a pull request for review when it finishes. " +
			"Use it when the user asks for work to be carried out rather than answered in chat.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "What the task should accomplish, as a single instruction.",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Target repository as \"owner/name\". Omit to use this instance's default repository. A repository this instance is not configured for is refused.",
				},
				"workflow": map[string]any{
					"type":        "string",
					"description": "Workflow to run (e.g. implement, tdd, feasibility). Omit to let the daemon route it.",
				},
			},
			"required": []any{"title"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			title := strings.TrimSpace(asString(input["title"]))
			if title == "" {
				return nil, fmt.Errorf("task_spawn: title is required")
			}

			id, err := creator.CreateTask(ctx, SpawnRequest{
				Title:    title,
				Repo:     strings.TrimSpace(asString(input["repo"])),
				Workflow: strings.TrimSpace(asString(input["workflow"])),
				// The bound identity, never input["identity"]. CreateTask
				// enforces the repository allow-list for it.
				Identity: identity,
			})
			if err != nil {
				return nil, fmt.Errorf("task_spawn: %w", err)
			}
			return TaskSpawnResult{
				ID:      id,
				Message: fmt.Sprintf("queued task %d: %s", id, title),
			}, nil
		},
	}
}

func taskActionTool(actor ChatTaskActor, identity string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "task_action",
		Toolset: "tasks",
		Description: "Perform an operator action on a task (e.g. abandon, archive, retry, stop, cancel, approve, reject). " +
			"Use it when the user asks to close, abandon, archive, retry, stop or approve a task.",
		Classification: tools.ClassMutating | tools.RequiresApproval,
		BuildApprovalDescription: func(input map[string]any) string {
			action := strings.TrimSpace(asString(input["action"]))
			taskID, _ := asInt64(input["task_id"])
			return fmt.Sprintf("Apply action %q to task %d.", action, taskID)
		},
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "integer",
					"description": "ID of the task to act on.",
				},
				"action": map[string]any{
					"type":        "string",
					"description": "The action to perform: abandon, archive, retry, stop, cancel, approve, reject.",
				},
			},
			"required": []any{"task_id", "action"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			taskID, ok := asInt64(input["task_id"])
			if !ok {
				return nil, fmt.Errorf("task_action: task_id is required")
			}
			actionStr := strings.TrimSpace(strings.ToLower(asString(input["action"])))
			if actionStr == "" {
				return nil, fmt.Errorf("task_action: action is required")
			}

			// The bound identity, never input["identity"].
			result, err := actor.ApplyChatTaskAction(ctx, identity, taskID, taskstate.Action(actionStr))
			if err != nil {
				return nil, fmt.Errorf("task_action: %w", err)
			}
			return result, nil
		},
	}
}

func taskLogsTool(reader ChatTaskLogReader, identity string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "task_logs",
		Toolset: "tasks",
		Description: "Read a task's persisted log history -- gate output, agent activity, errors -- " +
			"to answer questions like \"why did task N park?\" or \"what happened on task N?\" " +
			"without an operator needing to open the dashboard. " +
			"Defaults to the task's latest attempt when attempt is omitted. " +
			fmt.Sprintf("One call returns at most %d entries. ", maxTaskLogLimit) +
			"Two separate signals report incompleteness and they mean different things: " +
			"more_available means this call left matching entries unread, so pass its " +
			"cursor back as after_id to continue; truncated means the log is larger than " +
			"the readable window, so its OLDEST entries cannot be reached by paging at all. " +
			"If truncated is true, say so rather than implying you read the whole log, and " +
			"use send_file when the operator wants the complete archive.",
		Classification: tools.ClassIdempotent,
		Schema:         taskLogsSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			taskID, ok := asInt64(input["task_id"])
			if !ok {
				return nil, fmt.Errorf("task_logs: task_id is required")
			}
			attempt := 0
			if v, ok := asInt64(input["attempt"]); ok {
				attempt = int(v)
			}
			limit := tools.ListLimit(input, defaultTaskLogLimit, maxTaskLogLimit)

			levels := asStringSlice(input["level"])
			since, err := asRFC3339(input["since"])
			if err != nil {
				return nil, fmt.Errorf("task_logs: since: %w", err)
			}
			until, err := asRFC3339(input["until"])
			if err != nil {
				return nil, fmt.Errorf("task_logs: until: %w", err)
			}
			var afterID int64
			if v, ok := asInt64(input["after_id"]); ok {
				afterID = v
			}

			// The bound identity, never input["identity"].
			result, err := reader.ReadChatTaskLogs(ctx, identity, taskID, attempt, ChatTaskLogQuery{
				Component: strings.TrimSpace(asString(input["component"])),
				Contains:  strings.TrimSpace(asString(input["q"])),
				Levels:    levels,
				Since:     since,
				Until:     until,
				AfterID:   afterID,
				Limit:     limit,
			})
			if err != nil {
				return nil, fmt.Errorf("task_logs: %w", err)
			}
			return result, nil
		},
	}
}

// taskLogsSchema is the task_logs input schema. It lives apart from the
// tool entry only because the literal is long; it is pure data, and the
// handler is the sole consumer.
func taskLogsSchema() tools.JSONSchema {
	return tools.JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "integer",
				"description": "ID of the task to read logs for.",
			},
			"attempt": map[string]any{
				"type":        "integer",
				"description": "Which attempt to read. Omit for the task's current/latest attempt.",
			},
			"component": map[string]any{
				"type":        "string",
				"description": "Only return entries from this component (e.g. \"gate\"). Omit for all components.",
			},
			"q": map[string]any{
				"type":        "string",
				"description": "Only return entries whose message or fields contain this text (case-insensitive).",
			},
			"level": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only return entries whose level matches one of these (case-insensitive). Omit for all levels.",
			},
			"since": map[string]any{
				"type":        "string",
				"format":      "date-time",
				"description": "RFC3339 timestamp; exclude entries strictly before this. Omit for no lower bound.",
			},
			"until": map[string]any{
				"type":        "string",
				"format":      "date-time",
				"description": "RFC3339 timestamp; exclude entries strictly after this. Omit for no upper bound.",
			},
			"after_id": map[string]any{
				"type":        "integer",
				"description": "Opaque cursor from a previous task_logs result's \"cursor\" field. Omit for the first page.",
			},
			"limit": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Maximum entries to return. Defaults to %d, capped at %d.",
					defaultTaskLogLimit, maxTaskLogLimit,
				),
			},
		},
		"required": []any{"task_id"},
	}
}

// asStringSlice reads a JSON array of strings, tolerating absent or
// non-array values by returning nil. A model that emits a single string
// instead of an array is normalised to a one-element slice.
func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if s := strings.TrimSpace(t); s != "" {
			return []string{s}
		}
	}
	return nil
}

// asRFC3339 parses an RFC3339 timestamp string, treating the zero value
// as "no bound" (returns time.Time{} without error). An obviously
// malformed value is rejected so a model that emits nonsense does not
// silently get every entry. The parser accepts RFC3339Nano (with
// fractional seconds) as well as bare RFC3339 -- the schema advertises
// "RFC3339 timestamp" and per Go's time package the two layouts both
// match when no fractional component is present, but RFC3339 alone
// rejects the fractional case. RFC3339Nano is a strict superset.
func asRFC3339(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q: %w", s, err)
	}
	return t, nil
}

// asInt64 reads an integer field. JSON numbers decode as float64, but a
// model that emits an integer through a different provider path can arrive
// as int or int64, so all three are accepted.
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

// taskListLimit resolves the requested limit, falling back to the default for
// anything absent or non-positive and clamping the rest.
//
// JSON numbers decode as float64, but a model that emits an integer through a
// different provider path can arrive as int, so both are accepted.
func taskListLimit(input map[string]any) int {
	return tools.ListLimit(input, defaultTaskListLimit, maxTaskListLimit)
}

// asString reads a string field, tolerating an absent or wrongly-typed value
// rather than failing the call over it.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
