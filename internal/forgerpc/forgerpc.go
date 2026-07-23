// Package forgerpc lets archie-agent call the four forge.Forge methods
// workflow stages invoke mid-run (Comment, CloseIssue, CreatePR,
// SetStateLabel) over core NATS request/reply, instead of the agent
// container holding a live forge API token. archied remains the sole
// holder of forge credentials and the sole caller of forge.Forge.
//
// Only these four methods are proxied — the rest of forge.Forge (issue
// polling, invitations, reactions, PR-state reconciliation) is used
// exclusively by the daemon's own poll/reconcile loops, never from inside
// a workflow stage, so there's nothing for archie-agent to call.
package forgerpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/forge"
)

const (
	SubjectComment       = "archie.forge.comment"
	SubjectCloseIssue    = "archie.forge.close_issue"
	SubjectCreatePR      = "archie.forge.create_pr"
	SubjectSetStateLabel = "archie.forge.set_state_label"
)

type CommentRequest struct {
	Owner, Repo string
	Number      int
	Body        string
}

type CommentResponse struct {
	ID    int64  `json:"id"`
	Error string `json:"error,omitempty"`
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
	Number int    `json:"number"`
	Error  string `json:"error,omitempty"`
}

type SetStateLabelRequest struct {
	Owner, Repo string
	Number      int
	Label       string
	KnownLabels []string
}

// Response is a bare success/error envelope for calls with no return value.
type Response struct {
	Error string `json:"error,omitempty"`
}

// Server proxies forgerpc requests to a real forge.Forge implementation.
type Server struct {
	Forge forge.Forge
	Log   *slog.Logger
}

// Register subscribes all four handlers on nc. The returned func
// unsubscribes all of them.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	type registration struct {
		subject string
		handler nats.MsgHandler
	}
	regs := []registration{
		{SubjectComment, s.handleComment},
		{SubjectCloseIssue, s.handleCloseIssue},
		{SubjectCreatePR, s.handleCreatePR},
		{SubjectSetStateLabel, s.handleSetStateLabel},
	}

	var subs []*nats.Subscription
	for _, r := range regs {
		sub, err := nc.Subscribe(r.subject, r.handler)
		if err != nil {
			for _, s := range subs {
				s.Unsubscribe()
			}
			return nil, fmt.Errorf("subscribe %s: %w", r.subject, err)
		}
		subs = append(subs, sub)
	}
	return func() {
		for _, s := range subs {
			s.Unsubscribe()
		}
	}, nil
}

func (s *Server) handleComment(msg *nats.Msg) {
	var req CommentRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, CommentResponse{Error: fmt.Sprintf("decode comment request: %v", err)})
		return
	}
	id, err := s.Forge.Comment(context.Background(), req.Owner, req.Repo, req.Number, req.Body)
	resp := CommentResponse{ID: id}
	if err != nil {
		resp.Error = err.Error()
	}
	s.respond(msg, resp)
}

func (s *Server) handleCloseIssue(msg *nats.Msg) {
	var req CloseIssueRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Error: fmt.Sprintf("decode close_issue request: %v", err)})
		return
	}
	err := s.Forge.CloseIssue(context.Background(), req.Owner, req.Repo, req.Number, req.Comment)
	s.respond(msg, errResponse(err))
}

func (s *Server) handleCreatePR(msg *nats.Msg) {
	var req CreatePRRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, CreatePRResponse{Error: fmt.Sprintf("decode create_pr request: %v", err)})
		return
	}
	num, err := s.Forge.CreatePR(context.Background(), req.Owner, req.Repo, req.Title, req.Head, req.Base, req.Body)
	resp := CreatePRResponse{Number: num}
	if err != nil {
		resp.Error = err.Error()
	}
	s.respond(msg, resp)
}

func (s *Server) handleSetStateLabel(msg *nats.Msg) {
	var req SetStateLabelRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, Response{Error: fmt.Sprintf("decode set_state_label request: %v", err)})
		return
	}
	// SetStateLabel has no error return in forge.Forge — nothing to propagate.
	s.Forge.SetStateLabel(context.Background(), req.Owner, req.Repo, req.Number, req.Label, req.KnownLabels)
	s.respond(msg, Response{})
}

func errResponse(err error) Response {
	if err == nil {
		return Response{}
	}
	return Response{Error: err.Error()}
}

func (s *Server) respond(msg *nats.Msg, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("forgerpc: encode response failed", "err", err)
		}
		return
	}
	if err := msg.Respond(data); err != nil && s.Log != nil {
		s.Log.Error("forgerpc: respond failed", "err", err)
	}
}

// Client calls the forgerpc Server from archie-agent's process. It
// implements the four proxied forge.Forge methods with matching
// signatures so it can be used as a drop-in for workflow stages.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
}

func (c *Client) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	data, err := json.Marshal(CommentRequest{Owner: owner, Repo: repo, Number: number, Body: body})
	if err != nil {
		return 0, fmt.Errorf("encode comment request: %w", err)
	}
	reply, err := c.call(ctx, SubjectComment, data)
	if err != nil {
		return 0, err
	}
	var resp CommentResponse
	if err := json.Unmarshal(reply, &resp); err != nil {
		return 0, fmt.Errorf("forgerpc %s: decode response: %w", SubjectComment, err)
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	return resp.ID, nil
}

func (c *Client) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	data, err := json.Marshal(CloseIssueRequest{Owner: owner, Repo: repo, Number: number, Comment: comment})
	if err != nil {
		return fmt.Errorf("encode close_issue request: %w", err)
	}
	return c.callErr(ctx, SubjectCloseIssue, data)
}

func (c *Client) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	data, err := json.Marshal(CreatePRRequest{Owner: owner, Repo: repo, Title: title, Head: head, Base: base, Body: body})
	if err != nil {
		return 0, fmt.Errorf("encode create_pr request: %w", err)
	}
	reply, err := c.call(ctx, SubjectCreatePR, data)
	if err != nil {
		return 0, err
	}
	var resp CreatePRResponse
	if err := json.Unmarshal(reply, &resp); err != nil {
		return 0, fmt.Errorf("forgerpc %s: decode response: %w", SubjectCreatePR, err)
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	return resp.Number, nil
}

// SetStateLabel matches forge.Forge's signature (no error return). RPC or
// server-side failures are logged by the server; the daemon still owns
// SetStateLabel's fire-and-forget semantics.
func (c *Client) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	data, err := json.Marshal(SetStateLabelRequest{Owner: owner, Repo: repo, Number: number, Label: label, KnownLabels: knownLabels})
	if err != nil {
		return
	}
	_ = c.callErr(ctx, SubjectSetStateLabel, data)
}

func (c *Client) callErr(ctx context.Context, subject string, data []byte) error {
	reply, err := c.call(ctx, subject, data)
	if err != nil {
		return err
	}
	var resp Response
	if err := json.Unmarshal(reply, &resp); err != nil {
		return fmt.Errorf("forgerpc %s: decode response: %w", subject, err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

func (c *Client) call(ctx context.Context, subject string, data []byte) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	reply, err := c.Conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("forgerpc %s: %w", subject, err)
	}
	return reply.Data, nil
}
