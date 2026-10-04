// Package worktreerpc lets archie-agent ask archied to push the task branch
// over NATS, authorized by a per-dispatch grant.
package worktreerpc

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
	"github.com/samcharles93/archie-core/internal/natsrpc"
	"github.com/samcharles93/archie-core/internal/worktree"
)

const (
	SubjectPush = "archie.worktree.push"
)

// SubjectFor returns base scoped to identity, or base when identity is
// empty.
func SubjectFor(identity, base string) string {
	if identity == "" {
		return base
	}
	// Keyed by the identity's ID, as forgerpc.SubjectFor is.
	return "archie.worktree." + string(domainidentity.StableID(identity)) + "." + strings.TrimPrefix(base, "archie.worktree.")
}

type PushRequest struct {
	Grant string `json:"grant"`
}

type Response struct {
	natsrpc.Envelope
}

// Runs resolves a run credential to the task it was issued for. The State
// Store answers it, so a push survives a restart of archied mid-task.
type Runs interface {
	TaskForCredential(ctx context.Context, token string) (*workflow.Task, error)
}

// publishable returns the task a credential authorizes this identity's
// server to push for.
func publishable(ctx context.Context, runs Runs, token, identity string) (*workflow.Task, error) {
	if runs == nil || token == "" {
		return nil, errors.New("worktree publication grant is required")
	}
	task, err := runs.TaskForCredential(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("worktree publication grant is invalid: %w", err)
	}
	if task.Identity != identity || task.Branch == "" ||
		!worktree.ValidCoordinates(task.Owner, task.Repo, task.IssueNumber) {
		return nil, errors.New("worktree publication grant is invalid")
	}
	return task, nil
}

// defaultHandlerTimeout bounds one publication request when the Server has no
// explicit Timeout. A large repository push can be slow, so this is generous.
const defaultHandlerTimeout = 15 * time.Minute

// Server proxies push requests to a real worktree.Manager holding the
// forge push token.
type Server struct {
	Trees *worktree.Manager
	Runs  Runs
	Log   *slog.Logger
	// Timeout bounds a single publication. Zero uses
	// defaultHandlerTimeout.
	Timeout time.Duration
}

// handlerContext returns a context bounded by s.Timeout.
func (s *Server) handlerContext() (context.Context, context.CancelFunc) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultHandlerTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

// Register subscribes the publication handler under the root identity.
// RegisterFor subscribes it under one identity-scoped subject.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	return s.RegisterFor(nc, "")
}

// RegisterFor registers publication under the subject scoped to identity.
func (s *Server) RegisterFor(nc *nats.Conn, identity string) (unsubscribe func(), err error) {
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{
		{Subject: SubjectFor(identity, SubjectPush), Handler: func(msg *nats.Msg) { s.handlePush(identity, msg) }},
	})
}

func (s *Server) handlePush(identity string, msg *nats.Msg) {
	var req PushRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.respond(msg, err)
		return
	}
	ctx, cancel := s.handlerContext()
	defer cancel()
	task, err := publishable(ctx, s.Runs, req.Grant, identity)
	if err != nil {
		s.respond(msg, err)
		return
	}
	dir := s.Trees.Dir(task.Owner, task.Repo, task.IssueNumber)
	s.respond(msg, s.Trees.Push(ctx, dir, task.Branch))
}

func (s *Server) respond(msg *nats.Msg, err error) {
	natsrpc.Respond(msg, s.Log, "worktreerpc", Response{Envelope: natsrpc.NewEnvelope(err)})
}

// Client calls the worktreerpc Server from archie-agent's process.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
	// Identity scopes this client's calls to one identity's RPC server.
	// Empty uses the root subjects (single-identity deployments).
	Identity string
	Grant    string
}

func (c *Client) rpc() *natsrpc.Client { return &natsrpc.Client{Conn: c.Conn, Timeout: c.Timeout} }

func (c *Client) subject(base string) string { return SubjectFor(c.Identity, base) }

// Push asks archied to publish the branch bound to this client's dispatch
// grant using the daemon-held forge credential.
func (c *Client) Push(ctx context.Context) error {
	req := PushRequest{Grant: c.Grant}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), c.subject(SubjectPush), req)
	if err != nil {
		return err
	}
	return resp.Err()
}
