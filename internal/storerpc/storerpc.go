// Package storerpc lets archie-agent (which does not hold a database
// connection) mutate task state by calling back to archied over core NATS
// request/reply, instead of the agent process needing direct SQLite access.
// archied remains the sole owner of the store.Store.
package storerpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/store"
)

// Subjects for the two store.Store methods workflow stages call mid-run.
const (
	SubjectUpdate     = "archie.store.update"
	SubjectTransition = "archie.store.transition"
)

// UpdateRequest mirrors store.Store.Update's argument.
type UpdateRequest struct {
	Task *store.Task `json:"task"`
}

// TransitionRequest mirrors store.Store.Transition's arguments.
type TransitionRequest struct {
	TaskID int64  `json:"task_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Detail string `json:"detail"`
}

// Response carries the result of an RPC. Error is empty on success.
type Response struct {
	Error string `json:"error,omitempty"`
}

// Server handles storerpc requests against the daemon's Store.
type Server struct {
	Store *store.Store
	Log   *slog.Logger
}

// Register subscribes the update and transition handlers on nc. The
// returned func unsubscribes both.
func (s *Server) Register(nc *nats.Conn) (unsubscribe func(), err error) {
	updateSub, err := nc.Subscribe(SubjectUpdate, s.handleUpdate)
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", SubjectUpdate, err)
	}
	transitionSub, err := nc.Subscribe(SubjectTransition, s.handleTransition)
	if err != nil {
		updateSub.Unsubscribe()
		return nil, fmt.Errorf("subscribe %s: %w", SubjectTransition, err)
	}
	return func() {
		updateSub.Unsubscribe()
		transitionSub.Unsubscribe()
	}, nil
}

func (s *Server) handleUpdate(msg *nats.Msg) {
	var req UpdateRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.reply(msg, fmt.Errorf("decode update request: %w", err))
		return
	}
	s.reply(msg, s.Store.Update(context.Background(), req.Task))
}

func (s *Server) handleTransition(msg *nats.Msg) {
	var req TransitionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.reply(msg, fmt.Errorf("decode transition request: %w", err))
		return
	}
	s.reply(msg, s.Store.Transition(context.Background(), req.TaskID, req.From, req.To, req.Detail))
}

func (s *Server) reply(msg *nats.Msg, err error) {
	resp := Response{}
	if err != nil {
		resp.Error = err.Error()
	}
	data, encErr := json.Marshal(resp)
	if encErr != nil {
		if s.Log != nil {
			s.Log.Error("storerpc: encode response failed", "err", encErr)
		}
		return
	}
	if err := msg.Respond(data); err != nil && s.Log != nil {
		s.Log.Error("storerpc: respond failed", "err", err)
	}
}

// Client calls the storerpc Server from archie-agent's process.
type Client struct {
	Conn *nats.Conn
	// Timeout bounds each call when ctx has no deadline of its own.
	Timeout time.Duration
}

// Update calls store.Store.Update on the daemon over NATS request/reply.
func (c *Client) Update(ctx context.Context, task *store.Task) error {
	data, err := json.Marshal(UpdateRequest{Task: task})
	if err != nil {
		return fmt.Errorf("encode update request: %w", err)
	}
	return c.call(ctx, SubjectUpdate, data)
}

// Transition calls store.Store.Transition on the daemon over NATS request/reply.
func (c *Client) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	data, err := json.Marshal(TransitionRequest{TaskID: taskID, From: from, To: to, Detail: detail})
	if err != nil {
		return fmt.Errorf("encode transition request: %w", err)
	}
	return c.call(ctx, SubjectTransition, data)
}

func (c *Client) call(ctx context.Context, subject string, data []byte) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	reply, err := c.Conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("storerpc %s: %w", subject, err)
	}
	var resp Response
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		return fmt.Errorf("storerpc %s: decode response: %w", subject, err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}
