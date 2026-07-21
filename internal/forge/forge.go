// Package forge defines the interface archie uses to interact with a git
// host — polling issues, managing labels, opening PRs, and reacting to
// comments. GitHub is the primary implementation; other hosts (Gitea,
// GitLab) can be added later by implementing this interface.
package forge

import "context"

// Issue is a forge-neutral representation of an issue (not a PR).
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// Forge is the interface for interacting with a git host.
type Forge interface {
	// AcceptInvitations auto-accepts pending repository invitations.
	AcceptInvitations(ctx context.Context) error

	// AssignedIssues returns open issues assigned to the given user,
	// excluding PRs.
	AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error)

	// IssuesWithLabel returns open issues matching the given label,
	// excluding PRs. Used when Dispatch.Trigger is "label" or "either".
	IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error)

	// Comment posts an issue (or PR) comment and returns its id.
	Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error)

	// RepliesAfter returns comments on the issue with id > afterID that
	// were not written by exclude.
	RepliesAfter(ctx context.Context, owner, repo string, number int, afterID int64, exclude string) ([]Reply, error)

	// CreatePR opens a pull request and returns its number.
	CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error)

	// PRState returns "open", "merged", or "closed" for a PR.
	PRState(ctx context.Context, owner, repo string, number int) (string, error)

	// CloseIssue closes an issue with an optional final comment.
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error

	// React adds an emoji reaction to an issue.
	React(ctx context.Context, owner, repo string, number int, reaction string) error

	// SetStateLabel makes label the issue's only state label, removing any
	// other label that appears in knownLabels first. An empty label clears
	// all state labels (terminal states).
	SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string)

	// VerifyPush confirms the token can push to the repo.
	VerifyPush(ctx context.Context, owner, repo string) error
}
