package prbench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/hashicorp/go-hclog"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/infrastructure/forgeext"
	"github.com/samcharles93/archie-core/internal/infrastructure/prsource"
)

func newBenchmarkReviewer(ctx context.Context, opts Options, cfg config.Config, rt *runtime.Runtime) (bench.Reviewer, error) {
	if opts.Fixtures != "" {
		return fixtureReviewer{dir: opts.Fixtures}, nil
	}
	if opts.ReviewModel == "" || opts.ClassifyModel == "" {
		return nil, fmt.Errorf("live review requires both -review-model and -classification-model")
	}
	client, err := openGitHubPlugin(ctx, opts)
	if err != nil {
		return nil, err
	}
	prReader, prOK := client.(forge.PullRequestReader)
	diffReader, diffOK := client.(forge.PullRequestDiffReader)
	archiveReader, archiveOK := client.(forge.RepoArchiveReader)
	if !prOK || !diffOK || !archiveOK {
		return nil, fmt.Errorf("GitHub client cannot read pull request snapshots")
	}
	cfg.Models = map[string]string{"review": opts.ReviewModel, "classification": opts.ClassifyModel}
	cfg.Review.ApproveBeforePost = false
	cfg.Review.PrecisionGate = false
	return &liveReviewer{
		cfg: cfg, agent: modelloop.NewLoopRunner(rt, slog.Default()),
		source: prsource.New(forgeSource{PullRequestReader: prReader, PullRequestDiffReader: diffReader, RepoArchiveReader: archiveReader}),
	}, nil
}

// openGitHubPlugin runs the GitHub forge extension binary named by
// opts.ForgePlugin for the live review's pull request reads.
func openGitHubPlugin(ctx context.Context, opts Options) (forge.Forge, error) {
	if opts.ForgePlugin == "" {
		return nil, fmt.Errorf("live review requires -forge-plugin, the GitHub forge extension binary")
	}
	binary, err := os.ReadFile(opts.ForgePlugin)
	if err != nil {
		return nil, fmt.Errorf("read forge plugin: %w", err)
	}
	sum := sha256.Sum256(binary)
	return forgeext.Open(ctx, extension.NewHost(hclog.NewNullLogger()), forgeext.Instance{
		Spec:  extension.Spec{Name: "prbench-github", Path: opts.ForgePlugin, SHA256: hex.EncodeToString(sum[:]), Env: nil},
		Host:  "https://github.com",
		Token: opts.GitHubToken,
	}, slog.Default())
}

type liveReviewer struct {
	cfg    config.Config
	agent  agentrun.Runner
	source workflow.PRSource
	nextID atomic.Int64
}

func (r *liveReviewer) Review(ctx context.Context, p bench.Problem) (bench.Review, error) {
	owner, repo, number, err := parsePRURL(p.PRURL)
	if err != nil {
		return bench.Review{}, err
	}
	slog.Info("benchmark review started", "problem", p.ID)
	tc := &workflow.TaskContext{
		Task: &workflow.Task{ID: r.nextID.Add(1), Attempt: 1, Owner: owner, Repo: repo, PRNumber: number, Workflow: "pr-review", Inputs: map[string]any{"depth": "deep"}},
		Cfg:  r.cfg, Agent: r.agent, PRSource: r.source, Log: slog.Default(),
	}
	decision, err := workflow.ReviewPullRequest(ctx, tc)
	if err != nil {
		return bench.Review{}, err
	}
	// Non-nil empty comments means a completed pipeline found nothing.
	if decision.Comments == nil {
		decision.Comments = []prreview.ScoredFinding{}
	}
	slog.Info("benchmark review completed", "problem", p.ID, "comments", len(decision.Comments), "skipped_phases", decision.SkippedPhases)
	return bench.Review{Comments: decision.Comments, SkippedPhases: decision.SkippedPhases}, nil
}

func parsePRURL(raw string) (string, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid pull request URL: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Scheme != "https" || u.Host != "github.com" || len(parts) != 4 || parts[2] != "pull" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", "", 0, fmt.Errorf("benchmark requires a GitHub pull request URL")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 || parts[0] == "" || parts[1] == "" {
		return "", "", 0, fmt.Errorf("invalid pull request URL")
	}
	return parts[0], parts[1], n, nil
}

type forgeSource struct {
	forge.PullRequestReader
	forge.PullRequestDiffReader
	forge.RepoArchiveReader
}

func (s forgeSource) GetPullRequest(ctx context.Context, owner, repo string, number int) (prsource.PullRequest, error) {
	p, err := s.PullRequestReader.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return prsource.PullRequest{}, err
	}
	return prsource.PullRequest{Title: p.Title, Body: p.Body, HeadSHA: p.HeadSHA}, nil
}
