// Package forge defines the interface archie uses to interact with a git
// host  --  polling issues, managing labels, opening PRs, and reacting to
// comments. GitHub and Gitea are the supported implementations.
package forge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// New creates a Forge implementation for the given type.
func New(forgeType, token, host string, log *slog.Logger) (Forge, error) {
	switch forgeType {
	case "github":
		return NewGitHub(token, host, log)
	case "gitea":
		return NewGitea(token, host, log)
	case "none", "off", "disabled", "":
		return NewNoop(log), nil
	default:
		return nil, fmt.Errorf("unsupported forge type %q (want github, gitea, or none)", forgeType)
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

// PullRequest is a forge-neutral summary of an existing pull request,
// carrying the inputs the operator-triggered reviewer needs: its identity,
// head/base refs and SHAs, and the title/body that serve as the review's
// issue text.
type PullRequest struct {
	Number  int
	Title   string
	Body    string
	HeadRef string
	BaseRef string
	HeadSHA string
	BaseSHA string
	State   string // "open", "merged", or "closed"
}

// PullRequestReader fetches an existing pull request's metadata. Forge
// implementations that cannot read PRs (the noop forge) do not implement it;
// callers type-assert and refuse review when the capability is absent.
type PullRequestReader interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error)
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

// ReviewState values for Review.State, normalised across forges.
const (
	ReviewStateApproved         = "approved"
	ReviewStateRequestedChanges = "requested_changes"
	ReviewStateCommented        = "commented"
	ReviewStateDismissed        = "dismissed"
)

// Review is a forge-neutral review of an existing PR: an approval, a
// request-changes, a plain comment, or a dismissal.
type Review struct {
	ID          int64
	Author      string
	State       string // one of the ReviewState* constants
	SubmittedAt time.Time
}

// ReviewComment is a forge-neutral review comment on an existing PR.
// Line is a location hint, not authoritative: the remediation agent reads
// the whole file. InReplyTo is the parent comment ID on GitHub; Gitea's
// inline comments are flat under a review, so it is 0 there.
type ReviewComment struct {
	ID        int64
	Author    string
	Body      string
	Path      string
	Line      int
	InReplyTo int64
	CreatedAt time.Time
}

// PullRequestReviewReader reads review activity on an existing PR. Forge
// implementations that cannot read reviews (the noop forge) do not implement
// it; callers type-assert and refuse when the capability is absent.
//
// sinceID is the dedup cursor: only entries with ID > sinceID are returned,
// so a poller and a webhook delivery of the same comment collapse on the same
// ID (the TaskEnvelope.IdempotencyKey pattern applied to reviews).
type PullRequestReviewReader interface {
	ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]Review, error)
	ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]ReviewComment, error)
	// ReplyToReview posts a reply to an existing review comment (commentID).
	// A summary for a whole review (e.g. "requested changes" addressed) is a
	// plain PR comment via Forge.Comment, not this method.
	ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error
}

// normalizeReviewState maps a GitHub review state string onto the neutral
// ReviewState* constants. Unknown states pass through lowercased.
func normalizeReviewState(s string) string {
	switch s {
	case "APPROVED":
		return ReviewStateApproved
	case "CHANGES_REQUESTED":
		return ReviewStateRequestedChanges
	case "COMMENTED":
		return ReviewStateCommented
	case "DISMISSED":
		return ReviewStateDismissed
	default:
		return strings.ToLower(s)
	}
}
