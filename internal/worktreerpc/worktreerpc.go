// Package worktreerpc lets archie-agent request the two worktree
// operations that touch the network — clone/fetch (Prepare) and push
// (Push) — over core NATS request/reply, instead of holding the forge
// credential itself. archied remains the sole holder of
// worktree.Manager.Token and performs both against the host worktree
// directory (the same files the agent container has bind-mounted at
// /data/worktree).
//
// CommitAll/Diff/ChangedFiles/ChangedLines are purely local git
// operations against the already-mounted worktree and don't need the
// credential, so they run directly inside archie-agent.
package worktreerpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/worktree"
)

const (
	SubjectPush    = "archie.worktree.push"
	SubjectPrepare = "archie.worktree.prepare"
)

// PushRequest identifies the task's worktree by owner/repo/issue rather
// than trusting a path from the (sandboxed) agent — the server resolves
// the directory itself via worktree.Manager.Dir.
type PushRequest struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Issue  int    `json:"issue"`
	Branch string `json:"branch"`
}

type Response struct {
	Error string `json:"error,omitempty"`
}

// PrepareRequest mirrors worktree.Manager.Prepare's arguments.
type PrepareRequest struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Base   string `json:"base"`
	Issue  int    `json:"issue"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels string `json:"labels"`
}

type PrepareResponse struct {
	Dir    string `json:"dir"`
	Branch string `json:"branch"`
	Error  string `json:"error,omitempty"`
}

// Server proxies push requests to a real worktree.Manager holding the
// forge push token.
type Server struct {
	Trees *worktree.Manager
	Log   *slog.Logger
}

// Register subscribes the push and prepare handlers on nc. The returned
// func unsubscribes both.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	pushSub, err := nc.Subscribe(SubjectPush, s.handlePush)
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", SubjectPush, err)
	}
	prepareSub, err := nc.Subscribe(SubjectPrepare, s.handlePrepare)
	if err != nil {
		pushSub.Unsubscribe()
		return nil, fmt.Errorf("subscribe %s: %w", SubjectPrepare, err)
	}
	return func() {
		pushSub.Unsubscribe()
		prepareSub.Unsubscribe()
	}, nil
}

func (s *Server) handlePush(msg *nats.Msg) {
	var req PushRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, fmt.Errorf("decode push request: %w", err))
		return
	}
	dir := s.Trees.Dir(req.Owner, req.Repo, req.Issue)
	s.respond(msg, s.Trees.Push(context.Background(), dir, req.Branch))
}

func (s *Server) handlePrepare(msg *nats.Msg) {
	var req PrepareRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respondPrepare(msg, "", "", fmt.Errorf("decode prepare request: %w", err))
		return
	}
	dir, branch, err := s.Trees.Prepare(context.Background(), req.Owner, req.Repo, req.Base, req.Issue, req.Title, req.Body, req.Labels)
	s.respondPrepare(msg, dir, branch, err)
}

func (s *Server) respondPrepare(msg *nats.Msg, dir, branch string, err error) {
	resp := PrepareResponse{Dir: dir, Branch: branch}
	if err != nil {
		resp.Error = err.Error()
	}
	data, encErr := json.Marshal(resp)
	if encErr != nil {
		if s.Log != nil {
			s.Log.Error("worktreerpc: encode response failed", "err", encErr)
		}
		return
	}
	if err := msg.Respond(data); err != nil && s.Log != nil {
		s.Log.Error("worktreerpc: respond failed", "err", err)
	}
}

func (s *Server) respond(msg *nats.Msg, err error) {
	resp := Response{}
	if err != nil {
		resp.Error = err.Error()
	}
	data, encErr := json.Marshal(resp)
	if encErr != nil {
		if s.Log != nil {
			s.Log.Error("worktreerpc: encode response failed", "err", encErr)
		}
		return
	}
	if err := msg.Respond(data); err != nil && s.Log != nil {
		s.Log.Error("worktreerpc: respond failed", "err", err)
	}
}

// Client calls the worktreerpc Server from archie-agent's process.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
}

// Push requests that archied push branch for owner/repo/issue's worktree
// using its held forge credential.
func (c *Client) Push(ctx context.Context, owner, repo string, issue int, branch string) error {
	data, err := json.Marshal(PushRequest{Owner: owner, Repo: repo, Issue: issue, Branch: branch})
	if err != nil {
		return fmt.Errorf("encode push request: %w", err)
	}
	reply, err := c.request(ctx, SubjectPush, data)
	if err != nil {
		return err
	}
	var resp Response
	if err := json.Unmarshal(reply, &resp); err != nil {
		return fmt.Errorf("worktreerpc %s: decode response: %w", SubjectPush, err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

// Prepare requests that archied clone/fetch the worktree for
// owner/repo/issue using its held forge credential, matching
// worktree.Manager.Prepare's signature.
func (c *Client) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error) {
	data, err := json.Marshal(PrepareRequest{Owner: owner, Repo: repo, Base: base, Issue: issue, Title: title, Body: body, Labels: labels})
	if err != nil {
		return "", "", fmt.Errorf("encode prepare request: %w", err)
	}
	reply, err := c.request(ctx, SubjectPrepare, data)
	if err != nil {
		return "", "", err
	}
	var resp PrepareResponse
	if err := json.Unmarshal(reply, &resp); err != nil {
		return "", "", fmt.Errorf("worktreerpc %s: decode response: %w", SubjectPrepare, err)
	}
	if resp.Error != "" {
		return "", "", errors.New(resp.Error)
	}
	return resp.Dir, resp.Branch, nil
}

func (c *Client) request(ctx context.Context, subject string, data []byte) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	reply, err := c.Conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("worktreerpc %s: %w", subject, err)
	}
	return reply.Data, nil
}
