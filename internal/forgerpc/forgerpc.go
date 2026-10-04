// Package forgerpc lets archie-agent call the workflow.Forger methods over
// NATS request/reply, so only archied holds forge credentials.
package forgerpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	domainidentity "github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/natsrpc"
)

const (
	SubjectComment             = "archie.forge.comment"
	SubjectCloseIssue          = "archie.forge.close_issue"
	SubjectCreatePR            = "archie.forge.create_pr"
	SubjectLinkBranch          = "archie.forge.link_branch"
	SubjectSetStateLabel       = "archie.forge.set_state_label"
	SubjectCreateReviewComment = "archie.forge.create_review_comments"
	SubjectReplyToReview       = "archie.forge.reply_to_review"
)

// Subjects returns every forge RPC subject identity's server answers: the
// set a task of that identity may publish to.
func Subjects(identity string) []string {
	bases := []string{
		SubjectComment, SubjectCloseIssue, SubjectCreatePR, SubjectLinkBranch,
		SubjectSetStateLabel, SubjectCreateReviewComment, SubjectReplyToReview,
	}
	out := make([]string, len(bases))
	for i, base := range bases {
		out[i] = SubjectFor(identity, base)
	}
	return out
}

// SubjectFor returns base scoped to identity, or base when identity is
// empty.
func SubjectFor(identity, base string) string {
	if identity == "" {
		return base
	}
	// Keyed by the identity's ID, not its name: a name may hold characters a
	// subject cannot, and both ends derive the same ID from the name.
	return "archie.forge." + string(domainidentity.StableID(identity)) + "." + strings.TrimPrefix(base, "archie.forge.")
}

// Target is what every request acts on and the credential that authorizes
// it: the run credential of the task asking, which only reaches that task's
// own repository.
type Target struct {
	Credential  string
	Owner, Repo string
}

// Runs resolves a run credential to the task it was issued for.
type Runs interface {
	TaskForCredential(ctx context.Context, token string) (*workflow.Task, error)
}

type CommentRequest struct {
	Target
	Number int
	Body   string
}

type CommentResponse struct {
	ID int64 `json:"id"`
	natsrpc.Envelope
}

type CloseIssueRequest struct {
	Target
	Number  int
	Comment string
}

type CreatePRRequest struct {
	Target
	Title, Head, Base, Body string
}

type CreatePRResponse struct {
	Number int `json:"number"`
	natsrpc.Envelope
}

type LinkBranchRequest struct {
	Target
	IssueNumber int
	Branch      string
}

type SetStateLabelRequest struct {
	Target
	Number      int
	Label       string
	KnownLabels []string
}

// CreateReviewCommentsRequest is one review's line-anchored comments.
type CreateReviewCommentsRequest struct {
	Target
	Number int
	// ReviewedHeadSHA is the revision the line numbers were measured on.
	ReviewedHeadSHA string
	Comments        []InlineReviewCommentPayload
}

// InlineReviewCommentPayload is one comment on the wire.
type InlineReviewCommentPayload struct {
	Path string
	Line int
	Body string
}

// ReplyToReviewRequest carries one threaded reply to a review comment.
type ReplyToReviewRequest struct {
	Target
	Number    int
	CommentID int64
	Body      string
}

// Response is a bare success/error envelope for calls with no return value.
type Response struct {
	natsrpc.Envelope
}

// Server proxies forgerpc requests to a real forge.Forge implementation.
type Server struct {
	Forge forge.Forge
	Runs  Runs
	Log   *slog.Logger
}

// Register subscribes all handlers on nc under the root (identity-less)
// subjects. RegisterFor(nc, identity) registers the same handlers under
// that identity's scoped subjects; the daemon calls it once per identity
// so container-mode tasks route to their own forge client.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	return s.RegisterFor(nc, "")
}

// RegisterFor registers all handlers under the subjects scoped to identity.
func (s *Server) RegisterFor(nc *nats.Conn, identity string) (unsubscribe func(), err error) {
	as := func(h func(string, *nats.Msg)) nats.MsgHandler { return func(msg *nats.Msg) { h(identity, msg) } }
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{
		{Subject: SubjectFor(identity, SubjectComment), Handler: as(s.handleComment)},
		{Subject: SubjectFor(identity, SubjectCloseIssue), Handler: as(s.handleCloseIssue)},
		{Subject: SubjectFor(identity, SubjectCreatePR), Handler: as(s.handleCreatePR)},
		{Subject: SubjectFor(identity, SubjectLinkBranch), Handler: as(s.handleLinkBranch)},
		{Subject: SubjectFor(identity, SubjectSetStateLabel), Handler: as(s.handleSetStateLabel)},
		{Subject: SubjectFor(identity, SubjectCreateReviewComment), Handler: as(s.handleCreateReviewComments)},
		{Subject: SubjectFor(identity, SubjectReplyToReview), Handler: as(s.handleReplyToReview)},
	})
}

// authorize checks that target's credential belongs to a live run of this
// server's identity and that the run's repository is the one targeted.
func (s *Server) authorize(identity string, target Target) error {
	if s.Runs == nil || target.Credential == "" {
		return errors.New("forge request needs a run credential")
	}
	task, err := s.Runs.TaskForCredential(context.Background(), target.Credential)
	if err != nil {
		return fmt.Errorf("forge request credential is invalid: %w", err)
	}
	if task.Identity != identity || task.Owner == "" ||
		!strings.EqualFold(task.Owner, target.Owner) || !strings.EqualFold(task.Repo, target.Repo) {
		return errors.New("forge request is outside its run's repository")
	}
	return nil
}

func (s *Server) handleComment(identity string, msg *nats.Msg) {
	var req CommentRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, CommentResponse{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode comment request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, CommentResponse{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	id, err := s.Forge.Comment(context.Background(), req.Owner, req.Repo, req.Number, req.Body)
	s.respond(msg, CommentResponse{ID: id, Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleCloseIssue(identity string, msg *nats.Msg) {
	var req CloseIssueRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode close_issue request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	err := s.Forge.CloseIssue(context.Background(), req.Owner, req.Repo, req.Number, req.Comment)
	s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleCreatePR(identity string, msg *nats.Msg) {
	var req CreatePRRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, CreatePRResponse{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode create_pr request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, CreatePRResponse{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	num, err := s.Forge.CreatePR(context.Background(), req.Owner, req.Repo, req.Title, req.Head, req.Base, req.Body)
	s.respond(msg, CreatePRResponse{Number: num, Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleLinkBranch(identity string, msg *nats.Msg) {
	var req LinkBranchRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode link_branch request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	err := s.Forge.LinkBranch(context.Background(), req.Owner, req.Repo, req.IssueNumber, req.Branch)
	s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleCreateReviewComments(identity string, msg *nats.Msg) {
	var req CreateReviewCommentsRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode create_review_comments request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	writer, ok := s.Forge.(forge.ReviewCommentWriter)
	if !ok {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(errors.New("this forge cannot post review comments"))})
		return
	}
	comments := make([]forge.InlineReviewComment, 0, len(req.Comments))
	for _, cm := range req.Comments {
		comments = append(comments, forge.InlineReviewComment{Path: cm.Path, Line: cm.Line, Body: cm.Body})
	}
	err := writer.CreateReviewComments(context.Background(), req.Owner, req.Repo, req.Number, req.ReviewedHeadSHA, comments)
	s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleReplyToReview(identity string, msg *nats.Msg) {
	var req ReplyToReviewRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode reply_to_review request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	reader, ok := s.Forge.(forge.PullRequestReviewReader)
	if !ok {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(errors.New("this forge cannot reply to reviews"))})
		return
	}
	err := reader.ReplyToReview(context.Background(), req.Owner, req.Repo, req.Number, req.CommentID, req.Body)
	s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleSetStateLabel(identity string, msg *nats.Msg) {
	var req SetStateLabelRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode set_state_label request: %w", err))})
		return
	}
	if err := s.authorize(identity, req.Target); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
		return
	}
	s.Forge.SetStateLabel(context.Background(), req.Owner, req.Repo, req.Number, req.Label, req.KnownLabels)
	s.respond(msg, Response{})
}

func (s *Server) respond(msg *nats.Msg, v any) {
	natsrpc.Respond(msg, s.Log, "forgerpc", v)
}

// Client calls the forgerpc Server from archie-agent's process. It
// implements the four proxied forge.Forge methods with matching
// signatures so it can be used as a drop-in for workflow stages.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
	// Identity scopes this client's calls to one identity's RPC server.
	// Empty uses the root subjects (single-identity deployments).
	Identity string
	// Credential is the task's run credential, presented on every call.
	Credential string
}

func (c *Client) target(owner, repo string) Target {
	return Target{Credential: c.Credential, Owner: owner, Repo: repo}
}

func (c *Client) rpc() *natsrpc.Client { return &natsrpc.Client{Conn: c.Conn, Timeout: c.Timeout} }

func (c *Client) subject(base string) string { return SubjectFor(c.Identity, base) }

func (c *Client) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	req := CommentRequest{Target: c.target(owner, repo), Number: number, Body: body}
	resp, err := natsrpc.Call[CommentResponse](ctx, c.rpc(), c.subject(SubjectComment), req)
	if err != nil {
		return 0, err
	}
	if err := resp.Err(); err != nil {
		return 0, err
	}
	return resp.ID, nil
}

func (c *Client) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	req := CloseIssueRequest{Target: c.target(owner, repo), Number: number, Comment: comment}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), c.subject(SubjectCloseIssue), req)
	if err != nil {
		return err
	}
	return resp.Err()
}

func (c *Client) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	req := CreatePRRequest{Target: c.target(owner, repo), Title: title, Head: head, Base: base, Body: body}
	resp, err := natsrpc.Call[CreatePRResponse](ctx, c.rpc(), c.subject(SubjectCreatePR), req)
	if err != nil {
		return 0, err
	}
	if err := resp.Err(); err != nil {
		return 0, err
	}
	return resp.Number, nil
}

func (c *Client) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	req := LinkBranchRequest{Target: c.target(owner, repo), IssueNumber: issueNumber, Branch: branch}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), c.subject(SubjectLinkBranch), req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// SetStateLabel remains available for RPC compatibility with older workers;
// the current workflow deliberately never calls it.
func (c *Client) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	req := SetStateLabelRequest{Target: c.target(owner, repo), Number: number, Label: label, KnownLabels: knownLabels}
	_, _ = natsrpc.Call[Response](ctx, c.rpc(), c.subject(SubjectSetStateLabel), req)
}

// CreateReviewComments posts line-anchored comments on the PR through the
// daemon.
func (c *Client) CreateReviewComments(ctx context.Context, owner, repo string, number int, reviewedHeadSHA string, comments []workflow.ReviewComment) error {
	payload := make([]InlineReviewCommentPayload, 0, len(comments))
	for _, cm := range comments {
		payload = append(payload, InlineReviewCommentPayload{Path: cm.Path, Line: cm.Line, Body: cm.Body})
	}
	req := CreateReviewCommentsRequest{
		Target: c.target(owner, repo), Number: number,
		ReviewedHeadSHA: reviewedHeadSHA, Comments: payload,
	}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), c.subject(SubjectCreateReviewComment), req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// ReplyToReview posts a threaded reply to one review comment, on behalf of
// the remediate workflow.
func (c *Client) ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error {
	req := ReplyToReviewRequest{Target: c.target(owner, repo), Number: number, CommentID: commentID, Body: body}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), c.subject(SubjectReplyToReview), req)
	if err != nil {
		return err
	}
	return resp.Err()
}
