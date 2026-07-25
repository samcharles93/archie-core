// Package storerpc lets archie-agent (which does not hold a database
// connection) mutate task state by calling back to archied over core NATS
// request/reply, instead of the agent process needing direct SQLite access.
// archied remains the sole owner of the store.TaskStore.
package storerpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/natsrpc"
	"github.com/samcharles93/archie-core/internal/store"
)

// Subjects for the two store.TaskStore methods workflow stages call mid-run.
const (
	SubjectUpdate     = "archie.store.update"
	SubjectTransition = "archie.store.transition"
)

// UpdateRequest mirrors store.TaskStore.Update's argument.
type UpdateRequest struct {
	Task *store.Task `json:"task"`
}

// TransitionRequest mirrors store.TaskStore.Transition's arguments.
type TransitionRequest struct {
	TaskID int64  `json:"task_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Detail string `json:"detail"`
}

// Response carries the result of an RPC. Error is empty on success.
type Response struct {
	natsrpc.Envelope
}

// Server handles storerpc requests against the daemon's TaskStore.
type Server struct {
	Store store.TaskStore
	Log   *slog.Logger
}

// Register subscribes the update and transition handlers on nc. The
// returned func unsubscribes both.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{
		{Subject: SubjectUpdate, Handler: s.handleUpdate},
		{Subject: SubjectTransition, Handler: s.handleTransition},
	})
}

func (s *Server) handleUpdate(msg *nats.Msg) {
	var req UpdateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.reply(msg, err)
		return
	}
	s.reply(msg, s.Store.Update(context.Background(), req.Task))
}

func (s *Server) handleTransition(msg *nats.Msg) {
	var req TransitionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.reply(msg, err)
		return
	}
	s.reply(msg, s.Store.Transition(context.Background(), req.TaskID, req.From, req.To, req.Detail))
}

func (s *Server) reply(msg *nats.Msg, err error) {
	natsrpc.Respond(msg, s.Log, "storerpc", Response{Envelope: natsrpc.NewEnvelope(err)})
}

// Client calls the storerpc Server from archie-agent's process.
// It implements store.WorkflowStore (the narrow interface workflow stages
// need — just Update + Transition), not the full store.TaskStore.
type Client struct {
	Conn    *nats.Conn
	Timeout time.Duration // bounds each call when ctx has no deadline
}

func (c *Client) rpc() *natsrpc.Client { return &natsrpc.Client{Conn: c.Conn, Timeout: c.Timeout} }

// Update calls store.TaskStore.Update on the daemon over NATS request/reply.
func (c *Client) Update(ctx context.Context, task *store.Task) error {
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), SubjectUpdate, UpdateRequest{Task: task})
	if err != nil {
		return err
	}
	return resp.Err()
}

// Transition calls store.TaskStore.Transition on the daemon over NATS request/reply.
func (c *Client) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	req := TransitionRequest{TaskID: taskID, From: from, To: to, Detail: detail}
	resp, err := natsrpc.Call[Response](ctx, c.rpc(), SubjectTransition, req)
	if err != nil {
		return err
	}
	return resp.Err()
}

// Compile-time check: Client satisfies WorkflowStore.
var _ store.WorkflowStore = (*Client)(nil)
