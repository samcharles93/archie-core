// Package forge defines the interface archie uses to interact with a git
// host: polling issues, managing labels, opening PRs, and reacting to
// comments. Forge extensions implement it over the forge.v1 surface.
package forge

import (
	"context"
	"errors"
	"io"
	"time"
)

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

	// CloseIssue closes an issue with an optional final comment.
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error

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

	// ClosePR closes a pull request without merging, with an optional final
	// comment.
	ClosePR(ctx context.Context, owner, repo string, number int, comment string) error
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

// PullRequestDiffReader fetches a pull request's unified diff over the
// forge's HTTP API. Implementations that cannot read PRs do not implement
// it, matching PullRequestReader.
type PullRequestDiffReader interface {
	GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error)
}

// RepoArchiveReader fetches a gzipped tar archive of a repository at a ref
// over the forge's HTTP API, with no git-level credential involved: the
// caller reads the returned stream and closes it. Implementations that
// cannot read repository contents do not implement it.
type RepoArchiveReader interface {
	GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error)
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
	Body        string // the review's own top-level summary; empty for a bare verdict
	SubmittedAt time.Time
}

// ReviewComment is a forge-neutral review comment on an existing PR.
// Line is a location hint, not authoritative: the remediation agent reads
// the whole file. InReplyTo is the parent comment ID on GitHub; Gitea's
// inline comments are flat under a review, so it is 0 there.
type ReviewComment struct {
	ID int64
	// ReviewID is the review this comment belongs to; 0 for a standalone comment.
	ReviewID  int64
	Author    string
	Body      string
	Path      string
	Line      int
	InReplyTo int64
	CreatedAt time.Time
}

// PullRequestReviewReader reads review activity on a PR newer than sinceID.
type PullRequestReviewReader interface {
	ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]Review, error)
	ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]ReviewComment, error)
	// ReplyToReview posts a reply to an existing review comment (commentID).
	// A summary for a whole review (e.g. "requested changes" addressed) is a
	// plain PR comment via Forge.Comment, not this method.
	ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error
}

// InlineReviewComment is one line-anchored comment to post on a PR: the file,
// the line in the reviewed commit's version of it, and the body -- which may
// carry a fenced suggestion block the author can apply in one click.
type InlineReviewComment struct {
	Path string
	Line int
	Body string
}

// ReviewCommentWriter posts line-anchored review comments on a PR, refusing
// when the head has moved past reviewedHeadSHA. Empty reviewedHeadSHA skips
// the check.
type ReviewCommentWriter interface {
	CreateReviewComments(ctx context.Context, owner, repo string, number int, reviewedHeadSHA string, comments []InlineReviewComment) error
}

// WebhookEvent is one decoded webhook delivery. At most one field is set; none
// means a delivery the host does not act on.
type WebhookEvent struct {
	Issue         *IssueEvent
	Review        *ReviewEvent
	ReviewComment *ReviewCommentEvent
}

// IssueEvent is an issue webhook, carrying what eligibility needs.
type IssueEvent struct {
	Action      string
	State       string
	PullRequest bool
	Owner, Repo string
	Number      int
	Title, Body string
	Labels      []string
	Assignees   []string
}

// ReviewEvent is a submitted pull request review.
type ReviewEvent struct {
	Action      string
	Owner, Repo string
	PRNumber    int
	ReviewID    int64
	Author      string
	State       string
	Body        string
}

// ReviewCommentEvent is an inline pull request review comment.
type ReviewCommentEvent struct {
	Action      string
	Owner, Repo string
	PRNumber    int
	ReviewID    int64
	CommentID   int64
	Author      string
	Body        string
	Path        string
	Line        int
}

// ErrBadWebhook marks a delivery body the forge could not decode.
var ErrBadWebhook = errors.New("undecodable webhook payload")

// WebhookParser decodes webhook deliveries. Callers verify the delivery's
// signature first; implementations parse only what is authenticated.
type WebhookParser interface {
	ParseWebhook(ctx context.Context, headers map[string]string, body []byte) (WebhookEvent, error)
}
