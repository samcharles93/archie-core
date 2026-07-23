// Package worktreerpc lets archie-agent request a git push over core NATS
// request/reply instead of holding the forge push token itself. archied
// remains the sole holder of worktree.Manager.Token and performs the push
// against the host worktree directory (the same files the agent container
// has bind-mounted at /data/worktree).
//
// Only Push is proxied. Prepare/CommitAll/Diff/ChangedFiles/ChangedLines
// are local git operations against the already-mounted worktree and don't
// need the push credential, so they can run directly inside archie-agent.
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

const SubjectPush = "archie.worktree.push"

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

// Server proxies push requests to a real worktree.Manager holding the
// forge push token.
type Server struct {
	Trees *worktree.Manager
	Log   *slog.Logger
}

// Register subscribes the push handler on nc. The returned func unsubscribes it.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	sub, err := nc.Subscribe(SubjectPush, s.handlePush)
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", SubjectPush, err)
	}
	return func() { sub.Unsubscribe() }, nil
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
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	reply, err := c.Conn.RequestWithContext(ctx, SubjectPush, data)
	if err != nil {
		return fmt.Errorf("worktreerpc %s: %w", SubjectPush, err)
	}
	var resp Response
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		return fmt.Errorf("worktreerpc %s: decode response: %w", SubjectPush, err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}
