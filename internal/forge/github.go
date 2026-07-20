// Package forge wraps the GitHub API surface archied needs: polling
// labelled issues, accepting collaborator invitations, commenting, and
// opening pull requests. Gitea support later lands as a second
// implementation behind the same methods.
package forge

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/go-github/v78/github"
)

type Client struct {
	gh  *github.Client
	log *slog.Logger
}

func New(token string, log *slog.Logger) *Client {
	return &Client{gh: github.NewClient(nil).WithAuthToken(token), log: log}
}

// AcceptInvitations auto-accepts pending repository invitations so
// adding archie as a collaborator is all a human has to do.
func (c *Client) AcceptInvitations(ctx context.Context) error {
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
func (c *Client) AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]*github.Issue, error) {
	var out []*github.Issue
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
				out = append(out, is)
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}
	return out, nil
}

// Comment posts an issue (or PR) comment.
func (c *Client) Comment(ctx context.Context, owner, repo string, number int, body string) error {
	_, _, err := c.gh.Issues.CreateComment(ctx, owner, repo, number,
		&github.IssueComment{Body: github.Ptr(body)})
	return err
}

// CreatePR opens a pull request and returns its number.
func (c *Client) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	pr, _, err := c.gh.PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: github.Ptr(title),
		Head:  github.Ptr(head),
		Base:  github.Ptr(base),
		Body:  github.Ptr(body),
	})
	if err != nil {
		return 0, fmt.Errorf("create PR %s/%s: %w", owner, repo, err)
	}
	return pr.GetNumber(), nil
}

// PRState returns "open", "merged", or "closed" for a PR.
func (c *Client) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
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
func (c *Client) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	if comment != "" {
		if err := c.Comment(ctx, owner, repo, number, comment); err != nil {
			return err
		}
	}
	_, _, err := c.gh.Issues.Edit(ctx, owner, repo, number,
		&github.IssueRequest{State: github.Ptr("closed")})
	return err
}

// VerifyPush confirms the token can push to the repo (permission check
// at startup so misconfiguration surfaces before any work is claimed).
func (c *Client) VerifyPush(ctx context.Context, owner, repo string) error {
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
