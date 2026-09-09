package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

// PRReviewResult is the structured outcome of an operator-triggered PR
// review, returned to the channel that requested it. It mirrors
// workflow.ReviewReport without importing the domain package: the gateway
// states what it needs and the daemon adapts between the two (the
// ChatTaskActor pattern). Status is "completed", "not_run", or "skipped";
// a zero-finding "completed" result is a review that ran and cleared, and
// is structurally distinct from "not_run".
type PRReviewResult struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	Model   string `json:"model,omitempty"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	// Reason explains why a not_run/skipped review did not execute.
	Reason   string            `json:"reason,omitempty"`
	Findings []PRReviewFinding `json:"findings"`
}

// PRReviewFinding is one defect from an operator-triggered review.
type PRReviewFinding struct {
	File            string `json:"file"`
	Line            int    `json:"line"`
	Defect          string `json:"defect"`
	FailureScenario string `json:"failure_scenario,omitempty"`
	Verdict         string `json:"verdict"`
	Level           string `json:"level"`
	Category        string `json:"category,omitempty"`
	Blocking        bool   `json:"blocking"`
}

// ChatPRReviewer is the surface review_pr needs. The gateway states what it
// needs and the daemon supplies an adapter over the forge, worktree manager
// and reviewer runtime, so this package keeps its independence from
// internal/forge, internal/worktree and internal/domain/workflow.
//
// identity follows the ChatTaskActor convention: nil denotes an
// authenticated dashboard operator who may review any configured
// repository; a non-nil identity is a chat identity scoped to its own
// configured repositories.
type ChatPRReviewer interface {
	ReviewPR(ctx context.Context, identity *string, repo string, number int) (PRReviewResult, error)
}

// ReviewTools builds the review_pr chat tool. A nil reviewer omits the tool
// rather than registering one that always fails, so a daemon without
// operator review support advertises nothing.
func ReviewTools(reviewer ChatPRReviewer, identity string) []tools.ToolEntry {
	if reviewer == nil {
		return nil
	}
	return []tools.ToolEntry{reviewPRTool(reviewer, identity)}
}

func reviewPRTool(reviewer ChatPRReviewer, identity string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "review_pr",
		Toolset: "tasks",
		Description: "Run the adversarial reviewer against an existing pull request and return its structured findings. " +
			"Use it when the user asks to review a PR, re-check one after a push, or see what the reviewer found. " +
			"Only a confirmed error-level finding blocks; a clean result reports zero findings explicitly.",
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
			result, err := reviewer.ReviewPR(ctx, &identity, repo, int(number))
			if err != nil {
				return nil, fmt.Errorf("review_pr: %w", err)
			}
			return result, nil
		},
	}
}
