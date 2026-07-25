// Package worktreerpc lets archie-agent request the two worktree
// operations that touch the network  --  clone/fetch (Prepare) and push
// (Push)  --  over core NATS request/reply, instead of holding the forge
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
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/natsrpc"
	"github.com/samcharles93/archie-core/internal/worktree"
)

const (
	SubjectPush    = "archie.worktree.push"
	SubjectPrepare = "archie.worktree.prepare"
)

// PushRequest identifies the task's worktree by owner/repo/issue rather
// than trusting a path from the (sandboxed) agent  --  the server resolves
// the directory itself via worktree.Manager.Dir.
type PushRequest struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Issue  int    `json:"issue"`
	Branch string `json:"branch"`
}

type Response struct {
	natsrpc.Envelope
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
	natsrpc.Envelope
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
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{
		{Subject: SubjectPush, Handler: s.handlePush},
		{Subject: SubjectPrepare, Handler: s.handlePrepare},
	})
}

func (s *Server) handlePush(msg *nats.Msg) {
	var req PushRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, err)
		return
	}
	dir := s.Trees.Dir(req.Owner, req.Repo, req.Issue)
	s.respond(msg, s.Trees.Push(context.Background(), dir, req.Branch))
}

func (s *Server) handlePrepare(msg *nats.Msg) {
	var req PrepareRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respondPrepare(msg, "", "", err)
		return
	}
	dir, branch, err := s.Trees.Prepare(context.Background(), req.Owner, req.Repo, req.Base, req.Issue, req.Title, req.Body, req.Labels)
	s.respondPrepare(msg, dir, branch, err)
}

func (s *Server) respondPrepare(msg *nats.Msg, dir, branch string, err error) {
	resp := PrepareResponse{Dir: dir, Branch: branch, Envelope: natsrpc.NewEnvelope(err)}
	natsrpc.Respond(msg, s.Log, "worktreerpc", resp)
}

func (s *Server) respond(msg *nats.Msg, err error) {
	natsrpc.Respond(msg, s.Log, "worktreerpc", Response{Envelope: natsrpc.NewEnvelope(err)})
}

// Client calls the worktreerpc Server from archie-agent's process.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
}

func (c *Client) rpc() *natsrpc.Client { return &natsrpc.Client{Conn: c.Conn, Timeout: c.Timeout} }

// Push requests that archied push branch for owner/repo/issue's worktree
// using its held forge credential.
func (c *Client) Push(ctx context.Context, owner, repo string, issue int, branch string) error {
	req := PushRequest{Owner: owner, Repo: repo, Issue: issue, Branch: branch}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), SubjectPush, req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// Prepare requests that archied clone/fetch the worktree for
// owner/repo/issue using its held forge credential, matching
// worktree.Manager.Prepare's signature.
func (c *Client) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error) {
	req := PrepareRequest{Owner: owner, Repo: repo, Base: base, Issue: issue, Title: title, Body: body, Labels: labels}
	resp, err := natsrpc.Call[PrepareResponse](ctx, c.rpc(), SubjectPrepare, req)
	if err != nil {
		return "", "", err
	}
	if err := resp.Err(); err != nil {
		return "", "", err
	}
	return resp.Dir, resp.Branch, nil
}
