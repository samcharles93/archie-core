// Package forge defines the interface archie uses to interact with a git
// host  --  polling issues, managing labels, opening PRs, and reacting to
// comments. GitHub and Gitea are the supported implementations.
package forge

import (
	"context"
	"fmt"
	"log/slog"
)

// New creates a Forge implementation for the given type.
func New(forgeType, token, host string, log *slog.Logger) (Forge, error) {
	switch forgeType {
	case "github":
		return NewGitHub(token, host, log)
	case "gitea":
		return NewGitea(token, host, log)
	default:
		return nil, fmt.Errorf("unsupported forge type %q (want github or gitea)", forgeType)
	}
}

// Issue is a forge-neutral representation of an issue (not a PR).
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// Forge is the interface for interacting with a git host.
type Forge interface {
	IssueForge
	PullRequestForge
	RepoForge
}

// IssueForge is issue-level operations.
type IssueForge interface {
	// AssignedIssues returns open issues assigned to the given user,
	// excluding PRs.
	AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error)

	// IssuesWithLabel returns open issues matching the given label,
	// excluding PRs.
	IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error)

	// Comment posts an issue (or PR) comment and returns its id.
	Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error)

	// RepliesAfter returns comments on the issue with id > afterID that
	// were not written by exclude.
	RepliesAfter(ctx context.Context, owner, repo string, number int, afterID int64, exclude string) ([]Reply, error)

	// CloseIssue closes an issue with an optional final comment.
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error

	// CreateIssue opens a new issue and returns its number.
	CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error)

	// React adds an emoji reaction to an issue.
	React(ctx context.Context, owner, repo string, number int, reaction string) error

	// SetStateLabel makes label the issue's only state label, removing any
	// other label that appears in knownLabels first. An empty label clears
	// all state labels (terminal states).
	SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string)
}

// PullRequestForge is pull-request-level operations.
type PullRequestForge interface {
	// CreatePR opens a pull request and returns its number.
	CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error)

	// PRState returns "open", "merged", or "closed" for a PR.
	PRState(ctx context.Context, owner, repo string, number int) (string, error)
}

// RepoForge is repository-level operations.
type RepoForge interface {
	// AcceptInvitations auto-accepts pending repository invitations.
	AcceptInvitations(ctx context.Context) error

	// VerifyPush confirms the token can push to the repo.
	VerifyPush(ctx context.Context, owner, repo string) error

	// LinkBranch associates a branch with an issue so Gitea shows the
	// development link in the issue sidebar.
	LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error
}
