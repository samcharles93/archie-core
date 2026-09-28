package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ReviewTools builds the review_pr chat tool. A nil creator omits the tool
// rather than registering one that always fails, so a daemon without a task
// store advertises nothing.
//
// The tool queues a pr-review task; it does not review in the turn. The
// pipeline needs a sandbox, a prefetched snapshot and minutes of model calls,
// so the operator gets a task ID back and watches the task like any other
// (docs/prds/pr-review-agent.md, "Triggers").
func ReviewTools(creator TaskCreator, identity string) []tools.ToolEntry {
	if creator == nil {
		return nil
	}
	return []tools.ToolEntry{reviewPRTool(creator, identity)}
}

// prReviewWorkflow is the workflow a queued operator review runs. Its
// declared interface requires pr_number, so the tool that names it is also
// the only chat path that can satisfy it.
const prReviewWorkflow = "pr-review"

func reviewPRTool(creator TaskCreator, identity string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "review_pr",
		Toolset: "tasks",
		Description: "Queue a review of an existing pull request. Use it when the user asks to review a PR, " +
			"re-check one after a push, or see what the reviewer found. The review runs as a task in its own " +
			"sandbox and posts its findings to the pull request, after operator approval when that is enabled; " +
			"this returns the task ID immediately.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository of the PR as \"owner/name\". Must be a repository this identity is configured for.",
				},
				"pr_number": map[string]any{
					"type":        "integer",
					"description": "Pull request number to review.",
				},
			},
			"required": []any{"repo", "pr_number"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			repo := strings.TrimSpace(asString(input["repo"]))
			if repo == "" {
				return nil, fmt.Errorf("review_pr: repo is required")
			}
			number, ok := asInt64(input["pr_number"])
			if !ok || number <= 0 {
				return nil, fmt.Errorf("review_pr: pr_number must be a positive integer")
			}
			id, err := creator.CreateTask(ctx, SpawnRequest{
				Title:    fmt.Sprintf("review %s#%d", repo, number),
				Body:     fmt.Sprintf("Operator-requested review of pull request %s#%d.", repo, number),
				Repo:     repo,
				Workflow: prReviewWorkflow,
				// int, not int64: task.Task.EffectivePRNumber reads the
				// input as json.Number, float64 or int, and an int64 would
				// resolve to 0 for a task that names a real PR.
				Inputs: map[string]any{"pr_number": int(number)},
				// The bound identity, never input["identity"]: CreateTask
				// enforces the repository allow-list for it.
				Identity: identity,
			})
			if err != nil {
				return nil, fmt.Errorf("review_pr: %w", err)
			}
			return TaskSpawnResult{
				ID:      id,
				Message: fmt.Sprintf("queued pr-review task %d for %s#%d", id, repo, number),
			}, nil
		},
	}
}
