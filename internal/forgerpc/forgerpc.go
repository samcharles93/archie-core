// Package forgerpc lets archie-agent call the forge methods workflow stages
// invoke mid-run  --  CloseIssue, CreatePR and LinkBranch, the workflow.Forger
// set  --  over core NATS request/reply, instead of the agent container holding
// a live forge API token. archied remains the sole holder of forge credentials
// and the sole caller of forge.Forge.
//
// The rest of forge.Forge (issue polling, invitations, reactions, PR-state
// reconciliation) is used exclusively by the daemon's own poll/reconcile
// loops, never from inside a workflow stage, so there is nothing for
// archie-agent to call.
//
// The server also still answers Comment and SetStateLabel. No current agent
// calls them; the handlers exist so an older archie-agent image keeps working
// against a newer daemon. The compatibility is one-directional: a NEW agent
// against an OLD daemon has no LinkBranch handler to reach, and the request
// times out, so an image skew that way parks tasks. Note it in release notes
// rather than assuming either side can lag.
package forgerpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/natsrpc"
)

const (
	SubjectComment       = "archie.forge.comment"
	SubjectCloseIssue    = "archie.forge.close_issue"
	SubjectCreatePR      = "archie.forge.create_pr"
	SubjectLinkBranch    = "archie.forge.link_branch"
	SubjectSetStateLabel = "archie.forge.set_state_label"
)

type CommentRequest struct {
	Owner, Repo string
	Number      int
	Body        string
}

type CommentResponse struct {
	ID int64 `json:"id"`
	natsrpc.Envelope
}

type CloseIssueRequest struct {
	Owner, Repo string
	Number      int
	Comment     string
}

type CreatePRRequest struct {
	Owner, Repo, Title, Head, Base, Body string
}

type CreatePRResponse struct {
	Number int `json:"number"`
	natsrpc.Envelope
}

type LinkBranchRequest struct {
	Owner, Repo string
	IssueNumber int
	Branch      string
}

type SetStateLabelRequest struct {
	Owner, Repo string
	Number      int
	Label       string
	KnownLabels []string
}

// Response is a bare success/error envelope for calls with no return value.
type Response struct {
	natsrpc.Envelope
}

// Server proxies forgerpc requests to a real forge.Forge implementation.
type Server struct {
	Forge forge.Forge
	Log   *slog.Logger
}

// Register subscribes all handlers on nc. The returned func
// unsubscribes all of them.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{
		{Subject: SubjectComment, Handler: s.handleComment},
		{Subject: SubjectCloseIssue, Handler: s.handleCloseIssue},
		{Subject: SubjectCreatePR, Handler: s.handleCreatePR},
		{Subject: SubjectLinkBranch, Handler: s.handleLinkBranch},
		{Subject: SubjectSetStateLabel, Handler: s.handleSetStateLabel},
	})
}

func (s *Server) handleComment(msg *nats.Msg) {
	var req CommentRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, CommentResponse{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode comment request: %w", err))})
		return
	}
	id, err := s.Forge.Comment(context.Background(), req.Owner, req.Repo, req.Number, req.Body)
	s.respond(msg, CommentResponse{ID: id, Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleCloseIssue(msg *nats.Msg) {
	var req CloseIssueRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode close_issue request: %w", err))})
		return
	}
	err := s.Forge.CloseIssue(context.Background(), req.Owner, req.Repo, req.Number, req.Comment)
	s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleCreatePR(msg *nats.Msg) {
	var req CreatePRRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, CreatePRResponse{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode create_pr request: %w", err))})
		return
	}
	num, err := s.Forge.CreatePR(context.Background(), req.Owner, req.Repo, req.Title, req.Head, req.Base, req.Body)
	s.respond(msg, CreatePRResponse{Number: num, Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleLinkBranch(msg *nats.Msg) {
	var req LinkBranchRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode link_branch request: %w", err))})
		return
	}
	err := s.Forge.LinkBranch(context.Background(), req.Owner, req.Repo, req.IssueNumber, req.Branch)
	s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(err)})
}

func (s *Server) handleSetStateLabel(msg *nats.Msg) {
	var req SetStateLabelRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Envelope: natsrpc.NewEnvelope(fmt.Errorf("decode set_state_label request: %w", err))})
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
}

func (c *Client) rpc() *natsrpc.Client { return &natsrpc.Client{Conn: c.Conn, Timeout: c.Timeout} }

// Comment and SetStateLabel are not in workflow.Forger, so no current agent
// calls them. They stay as the client half of handlers kept for image skew
// (see the package doc) -- and as the only way to exercise those handlers
// end to end, which is why removing them would leave the compatibility path
// untested rather than merely unused.
func (c *Client) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	req := CommentRequest{Owner: owner, Repo: repo, Number: number, Body: body}
	resp, err := natsrpc.Call[CommentResponse](ctx, c.rpc(), SubjectComment, req)
	if err != nil {
		return 0, err
	}
	if err := resp.Err(); err != nil {
		return 0, err
	}
	return resp.ID, nil
}

func (c *Client) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	req := CloseIssueRequest{Owner: owner, Repo: repo, Number: number, Comment: comment}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), SubjectCloseIssue, req)
	if err != nil {
		return err
	}
	return resp.Err()
}

func (c *Client) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	req := CreatePRRequest{Owner: owner, Repo: repo, Title: title, Head: head, Base: base, Body: body}
	resp, err := natsrpc.Call[CreatePRResponse](ctx, c.rpc(), SubjectCreatePR, req)
	if err != nil {
		return 0, err
	}
	if err := resp.Err(); err != nil {
		return 0, err
	}
	return resp.Number, nil
}

func (c *Client) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	req := LinkBranchRequest{Owner: owner, Repo: repo, IssueNumber: issueNumber, Branch: branch}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), SubjectLinkBranch, req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// SetStateLabel remains available for RPC compatibility with older workers;
// the current workflow deliberately never calls it.
func (c *Client) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	req := SetStateLabelRequest{Owner: owner, Repo: repo, Number: number, Label: label, KnownLabels: knownLabels}
	_, _ = natsrpc.Call[Response](ctx, c.rpc(), SubjectSetStateLabel, req)
}
