package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

const (
	// defaultTaskListLimit is how many tasks a bare task_list returns.
	defaultTaskListLimit = 20

	// maxTaskListLimit bounds what the model can ask for. A queue of a few
	// hundred tasks would otherwise land in the context window whole.
	maxTaskListLimit = 100
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
	PRNumber int    `json:"pr_number,omitempty"`
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

// TaskTools builds the chat tools that let Archie see and add to its own work
// queue. Until these existed, /spawn and the task list were slash commands only
// a human could type, so "what are you working on?" had no tool path at all.
//
// Both tools are bound to identity at construction. The model never supplies
// it: a model that could name an identity could read or file work belonging to
// another instance on the same host.
//
// A nil backend omits its tool rather than registering one that always fails,
// so a daemon without chat task support advertises nothing.
//
// Deliberately absent: task_approve and task_cancel. waiting_human is the one
// point in the task lifecycle where a person must act, and /approve is that
// gate. The text reaching a chat turn is untrusted -- a forge issue body or an
// inbound email can be quoted into it -- so a tool that approves Archie's own
// work would remove the only human checkpoint in the lifecycle. Cancel is
// destructive on the same untrusted path. Both remain slash commands.
func TaskTools(lister ChatTaskLister, creator TaskCreator, identity string) []tools.ToolEntry {
	var entries []tools.ToolEntry
	if lister != nil {
		entries = append(entries, taskListTool(lister, identity))
	}
	if creator != nil {
		entries = append(entries, taskSpawnTool(creator, identity))
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
						defaultTaskListLimit, maxTaskListLimit),
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
