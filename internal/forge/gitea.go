package forge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"code.gitea.io/sdk/gitea"
)

// GiteaClient implements Forge against a Gitea instance.
type GiteaClient struct {
	cli   *gitea.Client
	host  string
	token string
	log   *slog.Logger

	labelMu       sync.Mutex
	labelsEnsured map[string]bool
}

// NewGitea creates a Gitea-backed Forge implementation.
func NewGitea(token, host string, log *slog.Logger) (Forge, error) {
	host = strings.TrimRight(host, "/")
	cli, err := gitea.NewClient(host, gitea.SetToken(token))
	if err != nil {
		return nil, fmt.Errorf("gitea client %s: %w", host, err)
	}
	return &GiteaClient{cli: cli, host: host, token: token, log: log}, nil
}

// ── Forge interface ──────────────────────────────────────────────────

// AcceptInvitations auto-accepts pending repository invitations.
func (c *GiteaClient) AcceptInvitations(ctx context.Context) error {
	// Gitea doesn't have a direct equivalent to GitHub's user invitations
	// API. Repository collaborators are added directly by the owner.
	return nil
}

// AssignedIssues returns open issues assigned to the given user, excluding PRs.
func (c *GiteaClient) AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error) {
	opts := gitea.ListIssueOption{
		PageSize:   50,
		State:      gitea.StateOpen,
		AssignedBy: assignee,
	}
	var out []Issue
	for {
		issues, resp, err := c.cli.ListRepoIssues(owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues %s/%s: %w", owner, repo, err)
		}
		for _, is := range issues {
			if is.PullRequest == nil {
				out = append(out, Issue{
					Number: int(is.Index),
					Title:  is.Title,
					Body:   is.Body,
					Labels: labelNamesGitea(is.Labels),
				})
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// IssuesWithLabel returns open issues matching the given label, excluding PRs.
func (c *GiteaClient) IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error) {
	opts := gitea.ListIssueOption{
		PageSize: 50,
		State:    gitea.StateOpen,
		Labels:   []string{label},
	}
	var out []Issue
	for {
		issues, resp, err := c.cli.ListRepoIssues(owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues %s/%s: %w", owner, repo, err)
		}
		for _, is := range issues {
			if is.PullRequest == nil {
				out = append(out, Issue{
					Number: int(is.Index),
					Title:  is.Title,
					Body:   is.Body,
					Labels: labelNamesGitea(is.Labels),
				})
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// Comment posts an issue comment and returns its id.
func (c *GiteaClient) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	cm, _, err := c.cli.CreateIssueComment(owner, repo, int64(number), gitea.CreateIssueCommentOption{Body: body})
	if err != nil {
		return 0, err
	}
	return cm.ID, nil
}

// RepliesAfter returns comments on the issue with id > afterID not written
// by exclude.
func (c *GiteaClient) RepliesAfter(ctx context.Context, owner, repo string, number int, afterID int64, exclude string) ([]Reply, error) {
	comments, _, err := c.cli.ListIssueComments(owner, repo, int64(number), gitea.ListIssueCommentOptions{
		PageSize: 50,
	})
	if err != nil {
		return nil, err
	}
	var out []Reply
	for _, cm := range comments {
		if cm.ID <= afterID || cm.Poster.UserName == exclude {
			continue
		}
		out = append(out, Reply{ID: cm.ID, User: cm.Poster.UserName, Body: cm.Body})
	}
	return out, nil
}

// CreatePR opens a pull request and returns its number.
func (c *GiteaClient) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	pr, _, err := c.cli.CreatePullRequest(owner, repo, gitea.CreatePullRequestOption{
		Title: title,
		Head:  head,
		Base:  base,
		Body:  body,
	})
	if err != nil {
		return 0, fmt.Errorf("create PR %s/%s: %w", owner, repo, err)
	}
	return int(pr.Index), nil
}

// PRState returns "open", "merged", or "closed" for a PR.
// Returns "closed" for non-existent PRs (already merged/closed/removed).
func (c *GiteaClient) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
	pr, _, err := c.cli.GetPullRequest(owner, repo, int64(number))
	if err != nil {
		// "not found" means the PR was already merged/closed and cleaned up.
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "GetUserByName") {
			return "closed", nil
		}
		return "", err
	}
	if pr.HasMerged {
		return "merged", nil
	}
	return string(pr.State), nil
}

// GetPullRequest returns the forge-neutral metadata for an existing PR.
func (c *GiteaClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error) {
	pr, _, err := c.cli.GetPullRequest(owner, repo, int64(number))
	if err != nil {
		return PullRequest{}, fmt.Errorf("get pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	// A merged/closed PR whose head branch was deleted returns nil head/base
	// from Gitea; dereferencing it would panic.
	if pr.Head == nil || pr.Base == nil {
		return PullRequest{}, fmt.Errorf("pull request %s/%s#%d has a missing head or base ref", owner, repo, number)
	}
	state := string(pr.State)
	if pr.HasMerged {
		state = "merged"
	}
	return PullRequest{
		Number:  int(pr.Index),
		Title:   pr.Title,
		Body:    pr.Body,
		HeadRef: pr.Head.Ref,
		BaseRef: pr.Base.Ref,
		HeadSHA: pr.Head.Sha,
		BaseSHA: pr.Base.Sha,
		State:   state,
	}, nil
}

// ListReviews returns the reviews on a PR with ID > sinceID.
func (c *GiteaClient) ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]Review, error) {
	var out []Review
	opts := gitea.ListPullReviewsOptions{}
	for {
		reviews, resp, err := c.cli.ListPullReviews(owner, repo, int64(number), opts)
		if err != nil {
			return nil, fmt.Errorf("list reviews %s/%s#%d: %w", owner, repo, number, err)
		}
		for _, r := range reviews {
			if r.ID <= sinceID {
				continue
			}
			out = append(out, Review{
				ID:          r.ID,
				Author:      giteaReviewAuthor(r.Reviewer),
				State:       normalizeGiteaReviewState(r.State),
				SubmittedAt: r.Submitted,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// ListReviewComments returns the inline review comments on a PR with
// ID > sinceID. Gitea nests comments under reviews, so this lists each
// review's comments.
func (c *GiteaClient) ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]ReviewComment, error) {
	reviews, err := c.ListReviews(ctx, owner, repo, number, 0)
	if err != nil {
		return nil, err
	}
	var out []ReviewComment
	for _, rv := range reviews {
		comments, _, err := c.cli.ListPullReviewComments(owner, repo, int64(number), rv.ID)
		if err != nil {
			return nil, fmt.Errorf("list review comments %s/%s#%d review %d: %w", owner, repo, number, rv.ID, err)
		}
		for _, cm := range comments {
			if cm.ID <= sinceID {
				continue
			}
			out = append(out, ReviewComment{
				ID:        cm.ID,
				Author:    giteaReviewAuthor(cm.Reviewer),
				Body:      cm.Body,
				Path:      cm.Path,
				Line:      int(cm.LineNum),
				CreatedAt: cm.Created,
			})
		}
	}
	return out, nil
}

// ReplyToReview posts a reply to an existing review comment.
func (c *GiteaClient) ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error {
	if _, _, err := c.cli.CreatePullReviewCommentReply(owner, repo, int64(number), commentID, gitea.CreatePullReviewCommentReplyOptions{Body: body}); err != nil {
		return fmt.Errorf("reply to review comment %d on %s/%s#%d: %w", commentID, owner, repo, number, err)
	}
	return nil
}

// giteaReviewAuthor returns a review/comment author's login, or "" when the
// author is absent (a team review, or a deleted user).
func giteaReviewAuthor(u *gitea.User) string {
	if u == nil {
		return ""
	}
	return u.UserName
}

// normalizeGiteaReviewState maps a Gitea review state onto the neutral
// ReviewState* constants. Pending and request-review reviews are not a
// decision and pass through lowercased so the caller can ignore them.
func normalizeGiteaReviewState(s gitea.ReviewStateType) string {
	switch s {
	case gitea.ReviewStateApproved:
		return ReviewStateApproved
	case gitea.ReviewStateRequestChanges:
		return ReviewStateRequestedChanges
	case gitea.ReviewStateComment:
		return ReviewStateCommented
	default:
		return strings.ToLower(string(s))
	}
}

// CloseIssue closes an issue with an optional final comment.
func (c *GiteaClient) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	if comment != "" {
		if _, err := c.Comment(ctx, owner, repo, number, comment); err != nil {
			return err
		}
	}
	state := gitea.StateClosed
	_, _, err := c.cli.EditIssue(owner, repo, int64(number), gitea.EditIssueOption{State: &state})
	return err
}

// CreateIssue opens a new issue and returns its number.
func (c *GiteaClient) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error) {
	iss, _, err := c.cli.CreateIssue(owner, repo, gitea.CreateIssueOption{Title: title, Body: body})
	if err != nil {
		return 0, err
	}
	return int(iss.Index), nil
}

// React adds an emoji reaction to an issue.
func (c *GiteaClient) React(ctx context.Context, owner, repo string, number int, reaction string) error {
	_, _, err := c.cli.PostIssueReaction(owner, repo, int64(number), reaction)
	return err
}

// LinkBranch associates a branch with an issue via the Gitea API.
func (c *GiteaClient) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/issues/%d/refs", c.host, owner, repo, issueNumber)
	body := fmt.Sprintf(`{"ref": "%s"}`, branch)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("link branch: HTTP %d", resp.StatusCode)
	}
	return nil
}

// SetStateLabel makes label the issue's only state label.
func (c *GiteaClient) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	known := make(map[string]bool, len(knownLabels))
	for _, l := range knownLabels {
		known[l] = true
	}
	issue, _, err := c.cli.GetIssue(owner, repo, int64(number))
	if err != nil {
		c.log.Warn("set state label: get issue failed", "issue", number, "err", err)
		return
	}
	// Remove known state labels (except the target).
	for _, l := range issue.Labels {
		if l.Name == label {
			label = "" // already present
		} else if known[l.Name] {
			if _, err := c.cli.DeleteIssueLabel(owner, repo, int64(number), l.ID); err != nil {
				c.log.Warn("remove state label failed", "label", l.Name, "err", err)
			}
		}
	}
	if label == "" {
		return
	}
	// Find or create the target label and add it by ID.
	labelID := c.resolveLabelID(owner, repo, label)
	if labelID == 0 {
		return // ensureLabel already logged
	}
	if _, _, err := c.cli.AddIssueLabels(owner, repo, int64(number), gitea.IssueLabelsOption{
		Labels: []int64{labelID},
	}); err != nil {
		c.log.Warn("add state label failed", "label", label, "err", err)
	}
}

// resolveLabelID returns the ID of a label, creating it if necessary.
func (c *GiteaClient) resolveLabelID(owner, repo, name string) int64 {
	labels, _, err := c.cli.ListRepoLabels(owner, repo, gitea.ListLabelsOptions{PageSize: 100})
	if err != nil {
		c.log.Warn("list repo labels failed", "err", err)
		return 0
	}
	for _, l := range labels {
		if l.Name == name {
			return l.ID
		}
	}
	// Create the label.
	c.ensureLabel(owner, repo, name)
	// Re-list to get the new ID.
	labels, _, err = c.cli.ListRepoLabels(owner, repo, gitea.ListLabelsOptions{PageSize: 100})
	if err != nil {
		return 0
	}
	for _, l := range labels {
		if l.Name == name {
			return l.ID
		}
	}
	return 0
}

// VerifyPush confirms the token can push to the repo.
func (c *GiteaClient) VerifyPush(ctx context.Context, owner, repo string) error {
	r, _, err := c.cli.GetRepo(owner, repo)
	if err != nil {
		// Gitea SDK may return "GetUserByName" for a non-existent repo.
		if strings.Contains(err.Error(), "GetUserByName") {
			return fmt.Errorf("repo %s/%s not found or token lacks access", owner, repo)
		}
		return fmt.Errorf("get repo %s/%s: %w", owner, repo, err)
	}
	if !r.Permissions.Push {
		return fmt.Errorf("no push permission on %s/%s", owner, repo)
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────

func labelNamesGitea(labels []*gitea.Label) []string {
	var names []string
	for _, l := range labels {
		names = append(names, l.Name)
	}
	return names
}

// Gitea state label colours matching the GitHub palette.
var giteaStateLabelColors = map[string]string{
	"archie:queued":  "#bfd4f2",
	"archie:working": "#1d76db",
	"archie:waiting": "#fbca04",
	"archie:pr":      "#0e8a16",
	"archie:parked":  "#d93f0b",
}

func (c *GiteaClient) ensureLabel(owner, repo, name string) {
	key := owner + "/" + repo + "/" + name
	c.labelMu.Lock()
	seen := c.labelsEnsured[key]
	c.labelMu.Unlock()
	if seen {
		return
	}
	color := giteaStateLabelColors[name]
	if color == "" {
		color = "#bfd4f2"
	}
	_, _, err := c.cli.CreateLabel(owner, repo, gitea.CreateLabelOption{
		Name:        name,
		Color:       color,
		Description: "archie task state (managed automatically)",
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "422") {
		c.log.Warn("create label failed", "label", name, "err", err)
		return
	}
	c.labelMu.Lock()
	if c.labelsEnsured == nil {
		c.labelsEnsured = map[string]bool{}
	}
	c.labelsEnsured[key] = true
	c.labelMu.Unlock()
}
