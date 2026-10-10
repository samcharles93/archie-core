package prbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
	"github.com/samcharles93/archie-core/internal/infrastructure/prsource"
)

// liveReviewer runs the production pr-review decision phases on one problem.
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
	if u.Scheme != "https" || u.Host != "github.com" || len(parts) != 4 || parts[2] != "pull" {
		return "", "", 0, fmt.Errorf("benchmark requires a GitHub pull request URL")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return "", "", 0, fmt.Errorf("invalid pull request URL")
	}
	return parts[0], parts[1], n, nil
}

// publicGitHub reads the benchmark's public pull requests anonymously. The
// diff and archive come from hosts outside the API rate limit, so a full run
// makes one API call per pull request. GH_TOKEN or GITHUB_TOKEN, when set,
// only lifts that API limit.
type publicGitHub struct {
	token string
	mu    sync.Mutex
	prs   map[string]prsource.PullRequest
}

func newPublicGitHub() *publicGitHub {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	return &publicGitHub{token: token, prs: map[string]prsource.PullRequest{}}
}

func (g *publicGitHub) get(ctx context.Context, rawURL string, api bool) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if api && g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	return resp.Body, nil
}

func (g *publicGitHub) GetPullRequest(ctx context.Context, owner, repo string, number int) (prsource.PullRequest, error) {
	key := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	g.mu.Lock()
	pr, ok := g.prs[key]
	g.mu.Unlock()
	if ok {
		return pr, nil
	}
	body, err := g.get(ctx, fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, number), true)
	if err != nil {
		return prsource.PullRequest{}, err
	}
	defer body.Close()
	var raw struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		return prsource.PullRequest{}, err
	}
	pr = prsource.PullRequest{Title: raw.Title, Body: raw.Body, HeadSHA: raw.Head.SHA}
	g.mu.Lock()
	g.prs[key] = pr
	g.mu.Unlock()
	return pr, nil
}

func (g *publicGitHub) GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error) {
	body, err := g.get(ctx, fmt.Sprintf("https://github.com/%s/%s/pull/%d.diff", owner, repo, number), false)
	if err != nil {
		return "", err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	return string(data), err
}

func (g *publicGitHub) GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error) {
	return g.get(ctx, fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", owner, repo, ref), false)
}
