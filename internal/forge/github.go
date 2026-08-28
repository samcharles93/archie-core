// Package forge wraps the GitHub API surface archied needs: polling
// labelled issues, accepting collaborator invitations, commenting, and
// opening pull requests. Gitea support later lands as a second
// implementation behind the same methods.
package forge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/go-github/v78/github"
)

// GitHubClient implements Forge against the GitHub API.
type GitHubClient struct {
	gh  *github.Client
	log *slog.Logger

	labelMu       sync.Mutex
	labelsEnsured map[string]bool
}

// NewGitHub creates a GitHub-backed Forge implementation. Non-github.com
// hosts are configured using GitHub Enterprise's API and upload URL conventions.
func NewGitHub(token, host string, log *slog.Logger) (Forge, error) {
	client := github.NewClient(nil)
	if strings.TrimRight(host, "/") != "https://github.com" {
		var err error
		client, err = client.WithEnterpriseURLs(host, host)
		if err != nil {
			return nil, fmt.Errorf("configure GitHub host %q: %w", host, err)
		}
	}
	return &GitHubClient{gh: client.WithAuthToken(token), log: log}, nil
}

// AcceptInvitations auto-accepts pending repository invitations so
// adding archie as a collaborator is all a human has to do.
func (c *GitHubClient) AcceptInvitations(ctx context.Context) error {
	invites, _, err := c.gh.Users.ListInvitations(ctx, nil)
	if err != nil {
		return fmt.Errorf("list invitations: %w", err)
	}
	for _, inv := range invites {
		if _, err := c.gh.Users.AcceptInvitation(ctx, inv.GetID()); err != nil {
			c.log.Warn("accept invitation failed", "repo", inv.GetRepo().GetFullName(), "err", err)
			continue
		}
		c.log.Info("accepted repository invitation", "repo", inv.GetRepo().GetFullName())
	}
	return nil
}

// AssignedIssues returns open issues assigned to the given user,
// excluding PRs. Assigning an issue to the bot is how work is handed to
// archie (tink-bot style); labels only influence workflow routing.
func (c *GitHubClient) AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error) {
	var out []Issue
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Assignee:    assignee,
		ListOptions: github.ListOptions{PerPage: 50},
	}
	for {
		issues, resp, err := c.gh.Issues.ListByRepo(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues %s/%s: %w", owner, repo, err)
		}
		for _, is := range issues {
			if !is.IsPullRequest() {
				out = append(out, Issue{
					Number: is.GetNumber(),
					Title:  is.GetTitle(),
					Body:   is.GetBody(),
					Labels: labelNames(is),
				})
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return out, nil
}

// labelNames extracts label names without flattening them, preserving
// valid label names that contain commas or leading/trailing whitespace.
func labelNames(is *github.Issue) []string {
	var names []string
	for _, l := range is.Labels {
		names = append(names, l.GetName())
	}
	return names
}

// IssuesWithLabel returns open issues matching the given label, excluding
// PRs. Used when Dispatch.Trigger is "label" or "either"  --  no assignee
// required, just the label.
func (c *GitHubClient) IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error) {
	var out []Issue
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{label},
		ListOptions: github.ListOptions{PerPage: 50},
	}
	for {
		issues, resp, err := c.gh.Issues.ListByRepo(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues %s/%s: %w", owner, repo, err)
		}
		for _, is := range issues {
			if !is.IsPullRequest() {
				out = append(out, Issue{
					Number: is.GetNumber(),
					Title:  is.GetTitle(),
					Body:   is.GetBody(),
					Labels: labelNames(is),
				})
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return out, nil
}

// Comment posts an issue (or PR) comment and returns its id, so a
// workflow can watch for replies that come after it.
func (c *GitHubClient) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	cm, _, err := c.gh.Issues.CreateComment(ctx, owner, repo, number,
		&github.IssueComment{Body: new(body)})
	if err != nil {
		return 0, err
	}
	return cm.GetID(), nil
}

// Reply is a human comment on a watched issue.
type Reply struct {
	ID   int64
	User string
	Body string
}

// RepliesAfter returns comments on the issue with id > afterID that were
// not written by exclude (the bot)  --  the human side of waiting_human.
func (c *GitHubClient) RepliesAfter(ctx context.Context, owner, repo string, number int, afterID int64, exclude string) ([]Reply, error) {
	comments, _, err := c.gh.Issues.ListComments(ctx, owner, repo, number,
		&github.IssueListCommentsOptions{PerPage: 50})
	if err != nil {
		return nil, err
	}
	var out []Reply
	for _, cm := range comments {
		if cm.GetID() <= afterID || cm.GetUser().GetLogin() == exclude {
			continue
		}
		out = append(out, Reply{ID: cm.GetID(), User: cm.GetUser().GetLogin(), Body: cm.GetBody()})
	}
	return out, nil
}

// CreatePR opens a pull request and returns its number.
func (c *GitHubClient) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	pr, _, err := c.gh.PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: new(title),
		Head:  new(head),
		Base:  new(base),
		Body:  new(body),
	})
	if err != nil {
		return 0, fmt.Errorf("create PR %s/%s: %w", owner, repo, err)
	}
	return pr.GetNumber(), nil
}

// PRState returns "open", "merged", or "closed" for a PR.
func (c *GitHubClient) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return "", err
	}
	if pr.GetMerged() {
		return "merged", nil
	}
	return pr.GetState(), nil
}

// CloseIssue closes an issue with a final comment (feasibility "won't do").
func (c *GitHubClient) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	if comment != "" {
		if _, err := c.Comment(ctx, owner, repo, number, comment); err != nil {
			return err
		}
	}
	_, _, err := c.gh.Issues.Edit(ctx, owner, repo, number,
		&github.IssueRequest{State: new("closed")})
	return err
}

// LinkBranch associates a branch with an issue (GitHub stub  --  Gitea is primary).
func (c *GitHubClient) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	return nil
}

// CreateIssue opens a new issue and returns its number.
func (c *GitHubClient) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error) {
	req := &github.IssueRequest{
		Title:  &title,
		Body:   &body,
		Labels: &labels,
	}
	iss, _, err := c.gh.Issues.Create(ctx, owner, repo, req)
	if err != nil {
		return 0, fmt.Errorf("create issue %s/%s: %w", owner, repo, err)
	}
	return iss.GetNumber(), nil
}

// State labels mirror the task lifecycle onto the forge so archie's
// status is visible at a glance. SQLite remains the source of truth;
// labels are the human-facing projection  --  and removing the parked
// label is the forge-native retry trigger.
// Label strings are configured via [dispatch.labels]; these are the
// built-in defaults used as a fallback when config is absent.

// stateLabelColors maps label name → colour for on-demand creation.
var stateLabelColors = map[string]string{
	"archie:queued":  "bfd4f2", // grey-blue
	"archie:working": "1d76db", // blue
	"archie:waiting": "fbca04", // yellow
	"archie:pr":      "0e8a16", // green
	"archie:parked":  "d93f0b", // orange-red
}

// defaultLabelColor is used for custom labels not in stateLabelColors.
const defaultLabelColor = "bfd4f2"

// React adds an emoji reaction to an issue  --  the instant "received"
// acknowledgement on pickup.
func (c *GitHubClient) React(ctx context.Context, owner, repo string, number int, reaction string) error {
	_, _, err := c.gh.Reactions.CreateIssueReaction(ctx, owner, repo, number, reaction)
	return err
}

// ensureLabel creates the state label in the repo if it doesn't exist.
func (c *GitHubClient) ensureLabel(ctx context.Context, owner, repo, name string) {
	key := owner + "/" + repo + "/" + name
	c.labelMu.Lock()
	seen := c.labelsEnsured[key]
	c.labelMu.Unlock()
	if seen {
		return
	}
	color := stateLabelColors[name]
	if color == "" {
		color = defaultLabelColor
	}
	_, _, err := c.gh.Issues.CreateLabel(ctx, owner, repo, &github.Label{
		Name:        new(name),
		Color:       new(color),
		Description: new("archie task state (managed automatically)"),
	})
	// 422 = already exists; both outcomes mean the label is available.
	if err != nil && !strings.Contains(err.Error(), "already_exists") {
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

// SetStateLabel makes label the issue's only state label, removing any
// other label found in knownLabels first. An empty label clears all
// state labels (terminal states).
func (c *GitHubClient) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	known := make(map[string]bool, len(knownLabels))
	for _, l := range knownLabels {
		known[l] = true
	}
	issue, _, err := c.gh.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		c.log.Warn("set state label: get issue failed", "issue", number, "err", err)
		return
	}
	for _, l := range issue.Labels {
		name := l.GetName()
		if name != label && known[name] {
			if _, err := c.gh.Issues.RemoveLabelForIssue(ctx, owner, repo, number, name); err != nil {
				c.log.Warn("remove state label failed", "label", name, "err", err)
			}
		}
		if name == label {
			label = "" // already present; nothing to add
		}
	}
	if label == "" {
		return
	}
	c.ensureLabel(ctx, owner, repo, label)
	if _, _, err := c.gh.Issues.AddLabelsToIssue(ctx, owner, repo, number, []string{label}); err != nil {
		c.log.Warn("add state label failed", "label", label, "err", err)
	}
}

// VerifyPush confirms the token can push to the repo (permission check
// at startup so misconfiguration surfaces before any work is claimed).
func (c *GitHubClient) VerifyPush(ctx context.Context, owner, repo string) error {
	r, _, err := c.gh.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("get repo %s/%s: %w", owner, repo, err)
	}
	perms := r.GetPermissions()
	if !perms["push"] {
		return fmt.Errorf("no push permission on %s/%s", owner, repo)
	}
	return nil
}
