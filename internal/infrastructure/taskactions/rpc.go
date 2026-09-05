package taskactions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/natsrpc"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// actionSubject is the request/reply subject the Gateway's ChatTaskActor
// client issues and the daemon's responder serves. The daemon owns task
// execution; the standalone Gateway never mutates the task store directly.
const actionSubject = "archie.gateway.task-action"

type actionRequest struct {
	Identity string           `json:"identity"`
	TaskID   int64            `json:"task_id"`
	Action   taskstate.Action `json:"action"`
}

type actionResponse struct {
	natsrpc.Envelope
}

// Register exposes only identity-scoped operator actions on the daemon. The
// daemon retains execution cancellation, retry policy, forge closure and event
// ownership; the Gateway's client reaches this responder over NATS.
// Handlers run with context.Background() (the storerpc convention): the
// daemon's lifecycle ctx is not request-scoped, and a cancelled boot ctx must
// not poison later requests.
func Register(nc *nats.Conn, service taskactions.Service, log *slog.Logger) (func(), error) {
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{{
		Subject: actionSubject,
		Handler: func(msg *nats.Msg) {
			var req actionRequest
			if err := json.Unmarshal(msg.Data, &req); err != nil {
				natsrpc.Respond(msg, log, "taskactions", actionResponse{Envelope: natsrpc.NewEnvelope(err)})
				return
			}
			err := service.Apply(context.Background(), &req.Identity, req.TaskID, req.Action)
			natsrpc.Respond(msg, log, "taskactions", actionResponse{Envelope: natsrpc.NewEnvelope(err)})
		},
	}})
}

// Client forwards Gateway task actions to their daemon owner over NATS.
type Client struct {
	Conn    *nats.Conn
	Timeout time.Duration // bounds each call when ctx has no deadline
}

func (c Client) rpc() *natsrpc.Client {
	return &natsrpc.Client{Conn: c.Conn, Timeout: c.Timeout}
}

// ApplyChatTaskAction sends an operator action to the daemon's responder.
func (c Client) ApplyChatTaskAction(ctx context.Context, identity string, id int64, action taskstate.Action) (gateway.TaskActionResult, error) {
	if c.Conn == nil {
		return gateway.TaskActionResult{}, fmt.Errorf("task action connection is unavailable")
	}
	resp, err := natsrpc.Call[actionResponse](ctx, c.rpc(), actionSubject, actionRequest{Identity: identity, TaskID: id, Action: action})
	if err != nil {
		return gateway.TaskActionResult{}, err
	}
	if err := resp.Err(); err != nil {
		return gateway.TaskActionResult{}, err
	}
	return gateway.TaskActionResult{TaskID: id, Action: string(action), Message: fmt.Sprintf("Applied %s to task %d.", action, id)}, nil
}
