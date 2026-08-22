// Package worktreerpc lets archie-agent request publication of the task branch
// over core NATS request/reply without holding the forge credential itself.
// archied has already prepared the bind-mounted worktree before the container
// starts. Publication is authorized by an opaque per-dispatch grant; sandbox
// input never selects a host path, repository, or branch.
//
// CommitAll/Diff/ChangedFiles/ChangedLines are purely local git
// operations against the already-mounted worktree and don't need the
// credential, so they run directly inside archie-agent.
package worktreerpc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/natsrpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/worktree"
)

const (
	SubjectPush = "archie.worktree.push"
)

// SubjectFor returns the subject for base, scoped to identity when set.
// An empty identity uses the root subject (single-identity deployments and
// agent images that predate identity routing). The daemon registers one
// server per identity on these scoped subjects so a container-mode task
// owned by a non-root identity has its push/prepare calls served by that
// identity's own worktree manager (its own credential and workdir).
func SubjectFor(identity, base string) string {
	if identity == "" {
		return base
	}
	return "archie.worktree." + identity + "." + strings.TrimPrefix(base, "archie.worktree.")
}

type PushRequest struct {
	Grant string `json:"grant"`
}

type Response struct {
	natsrpc.Envelope
}

type grant struct {
	identity    string
	owner, repo string
	issue       int
	branch      string
}

// Grants owns short-lived publication capabilities issued for one dispatched
// task. A grant is useful only on the identity-scoped server that issued it.
type Grants struct {
	mu     sync.RWMutex
	grants map[string]grant
}

func NewGrants() *Grants { return &Grants{grants: make(map[string]grant)} }

// Issue creates a per-dispatch grant and a revocation function.
func (g *Grants) Issue(task *store.Task) (string, func(), error) {
	if task == nil || task.ID <= 0 || task.Branch == "" ||
		!worktree.ValidCoordinates(task.Owner, task.Repo, task.IssueNumber) {
		return "", nil, errors.New("task is not ready for worktree publication")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate worktree grant: %w", err)
	}
	token := fmt.Sprintf("%x", raw)
	g.mu.Lock()
	g.grants[token] = grant{identity: task.Identity, owner: task.Owner, repo: task.Repo, issue: task.IssueNumber, branch: task.Branch}
	g.mu.Unlock()
	return token, func() {
		g.mu.Lock()
		delete(g.grants, token)
		g.mu.Unlock()
	}, nil
}

func (g *Grants) resolve(token, identity string) (grant, error) {
	if g == nil || token == "" {
		return grant{}, errors.New("worktree publication grant is required")
	}
	g.mu.RLock()
	item, ok := g.grants[token]
	g.mu.RUnlock()
	if !ok || item.identity != identity {
		return grant{}, errors.New("worktree publication grant is invalid")
	}
	return item, nil
}

// defaultHandlerTimeout bounds one publication request when the Server has no
// explicit Timeout. A large repository push can be slow, so this is generous.
const defaultHandlerTimeout = 15 * time.Minute

// Server proxies push requests to a real worktree.Manager holding the
// forge push token.
type Server struct {
	Trees  *worktree.Manager
	Grants *Grants
	Log    *slog.Logger
	// Timeout bounds a single publication. Zero uses
	// defaultHandlerTimeout.
	Timeout time.Duration
}

// handlerContext returns the context a single request runs under.
//
// NATS request/reply carries no deadline from the caller, and the work
// now runs in-process through go-git rather than as a killable child
// process, so the server has to impose its own bound. Without one an
// unresponsive forge holds this handler's goroutine forever.
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
	item, err := s.Grants.resolve(req.Grant, identity)
	if err != nil {
		s.respond(msg, err)
		return
	}
	dir := s.Trees.Dir(item.owner, item.repo, item.issue)
	s.respond(msg, s.Trees.Push(ctx, dir, item.branch))
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
