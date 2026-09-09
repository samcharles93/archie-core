package archied

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/app/agentworker"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// prReviewer implements gateway.ChatPRReviewer for the daemon. It fetches
// an existing PR's metadata, materialises its head in a fresh clone, and runs
// the same adversarial reviewer the workflow stage uses, all synchronously in
// the requesting turn. Authorization is identity-scoped (the task_spawn
// repository allow-list) and one review per PR runs at a time.
type prReviewer struct {
	forge     forge.PullRequestReader // nil = forge cannot read PRs
	trees     *worktree.Manager
	models    map[string]string
	providers map[string]agentexec.Provider
	maxSteps  int
	// allowed maps a chat identity to the "owner/name" repos it may review.
	// A nil identity is an authenticated dashboard operator, who may review
	// any configured repository.
	allowed map[string]map[string]bool
	log     *slog.Logger

	mu       sync.Mutex
	inflight map[string]struct{}
}

var _ gateway.ChatPRReviewer = (*prReviewer)(nil)

// ReviewPR fetches the PR, runs the isolated reviewer against its head, and
// returns the structured findings. It never half-reviews: a missing forge
// capability, an unresolvable head/base, or an absent reviewer model returns
// an error rather than a fabricated result.
func (r *prReviewer) ReviewPR(ctx context.Context, identity *string, repo string, number int) (gateway.PRReviewResult, error) {
	owner, name, err := splitOwnerRepo(repo)
	if err != nil {
		return gateway.PRReviewResult{}, err
	}
	if err := r.authorize(identity, owner, name); err != nil {
		return gateway.PRReviewResult{}, err
	}

	key := owner + "/" + name + "#" + strconv.Itoa(number)
	if !r.begin(key) {
		return gateway.PRReviewResult{}, fmt.Errorf("review already in progress for %s", key)
	}
	defer r.end(key)

	if r.forge == nil {
		return gateway.PRReviewResult{}, fmt.Errorf("forge does not support pull request review")
	}
	pr, err := r.forge.GetPullRequest(ctx, owner, name, number)
	if err != nil {
		return gateway.PRReviewResult{}, err
	}

	model := agentworker.ReviewerModel(r.models)
	if model == "" {
		return gateway.PRReviewResult{}, fmt.Errorf("reviewer model is not configured (set models.reviewer, or models.builder as a fallback)")
	}
	reviewer := agentworker.NewOperatorReviewer(r.models, r.providers)
	if reviewer == nil {
		return gateway.PRReviewResult{}, fmt.Errorf("no provider runtime configured for the reviewer model %q", model)
	}
	if r.trees == nil {
		return gateway.PRReviewResult{}, fmt.Errorf("worktree manager is not configured")
	}

	dir, cleanup, err := r.trees.CheckoutPR(ctx, owner, name, pr.HeadRef, pr.BaseRef)
	if err != nil {
		return gateway.PRReviewResult{}, err
	}
	defer cleanup()

	diff, err := r.trees.Diff(ctx, dir, pr.BaseRef)
	if err != nil {
		return gateway.PRReviewResult{}, fmt.Errorf("compute PR diff: %w", err)
	}

	snapshotDir, err := os.MkdirTemp("", "pr-review-snapshot-*")
	if err != nil {
		return gateway.PRReviewResult{}, fmt.Errorf("create review snapshot directory: %w", err)
	}
	defer os.RemoveAll(snapshotDir)
	if err := r.trees.Snapshot(ctx, dir, snapshotDir); err != nil {
		return gateway.PRReviewResult{}, fmt.Errorf("snapshot PR head: %w", err)
	}

	report := reviewer.Review(ctx, workflow.ReviewRequest{
		SnapshotDir: snapshotDir,
		Diff:        diff,
		IssueText:   prIssueText(pr),
		MaxSteps:    r.maxSteps,
	})
	r.logReview(key, model, report)
	return mapReviewResult(repo, number, model, report), nil
}

// authorize enforces the identity-scoped repository allow-list. A nil
// identity (dashboard operator) may review any configured repository.
func (r *prReviewer) authorize(identity *string, owner, repo string) error {
	full := owner + "/" + repo
	if identity == nil {
		for _, set := range r.allowed {
			if set[full] {
				return nil
			}
		}
		return fmt.Errorf("repository %q is not configured", full)
	}
	set, ok := r.allowed[*identity]
	if !ok {
		return fmt.Errorf("identity %q is not configured for PR review", *identity)
	}
	if !set[full] {
		return fmt.Errorf("repository %q is not configured for this identity", full)
	}
	return nil
}

func (r *prReviewer) begin(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inflight == nil {
		r.inflight = make(map[string]struct{})
	}
	if _, ok := r.inflight[key]; ok {
		return false
	}
	r.inflight[key] = struct{}{}
	return true
}

func (r *prReviewer) end(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.inflight, key)
}

func (r *prReviewer) logReview(key, model string, report workflow.ReviewReport) {
	if r.log == nil {
		return
	}
	blocking := 0
	for _, f := range report.Findings {
		if f.Blocking() {
			blocking++
		}
	}
	r.log.Info("operator PR review complete",
		"pr", key, "model", model, "status", string(report.Status),
		"findings", len(report.Findings), "blocking", blocking)
}

// splitOwnerRepo splits "owner/name" with the same validation the gateway's
// task_spawn allow-list uses.
func splitOwnerRepo(s string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(s, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", fmt.Errorf("repository %q must be owner/name", s)
	}
	return owner, repo, nil
}

// prIssueText renders the PR title/body as the review's issue text.
func prIssueText(pr forge.PullRequest) string {
	if pr.Body == "" {
		return pr.Title
	}
	return pr.Title + "\n\n" + pr.Body
}

// mapReviewResult adapts a workflow.ReviewReport into the gateway's
// channel-neutral result shape.
func mapReviewResult(repo string, number int, model string, report workflow.ReviewReport) gateway.PRReviewResult {
	out := gateway.PRReviewResult{
		Repo:     repo,
		Number:   number,
		Model:    model,
		Status:   string(report.Status),
		Summary:  report.Summary,
		Reason:   report.SkipReason,
		Findings: make([]gateway.PRReviewFinding, 0, len(report.Findings)),
	}
	for _, f := range report.Findings {
		out.Findings = append(out.Findings, gateway.PRReviewFinding{
			File:            f.File,
			Line:            f.Line,
			Defect:          f.Defect,
			FailureScenario: f.FailureScenario,
			Verdict:         string(f.Verdict),
			Level:           string(f.Level),
			Category:        string(f.Category),
			Blocking:        f.Blocking(),
		})
	}
	return out
}

// buildReviewAllowlist derives the identity-scoped review allow-list from the
// same profiles task_spawn uses, so a chat identity and the review command
// agree on which repositories the identity may touch.
func buildReviewAllowlist(cfg config.Config) map[string]map[string]bool {
	profiles, _ := chatTaskProfiles(cfg)
	out := make(map[string]map[string]bool, len(profiles))
	for _, p := range profiles {
		set := make(map[string]bool, len(p.Repos))
		for _, repo := range p.Repos {
			set[repo] = true
		}
		out[p.Identity] = set
	}
	return out
}

// prReviewer assembles the operator-triggered review capability, or nil when
// the forge cannot read PRs (the noop forge, or a forge with no PR read
// support). A review tool that can never run must not be advertised, matching
// the TaskTools nil-backend rule.
func (b *boot) prReviewer() gateway.ChatPRReviewer {
	forgeReader, _ := b.forgeClient.(forge.PullRequestReader)
	if forgeReader == nil {
		return nil
	}
	return &prReviewer{
		forge:     forgeReader,
		trees:     b.trees,
		models:    b.cfg.Models,
		providers: executionProviders(b.cfg),
		maxSteps:  b.cfg.Budgets.MaxSteps,
		allowed:   buildReviewAllowlist(b.cfg),
		log:       b.log,
	}
}
