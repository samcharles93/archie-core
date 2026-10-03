package taskactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/natsrpc"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// actionSubject is the request/reply subject the Gateway's ChatTaskActor
// client issues and the daemon's responder serves. The daemon owns task
// execution; the standalone Gateway never mutates the task store directly.
const actionSubject = "archie.gateway.task-action"

// actionRequest is a task action request. A nil Identity is a caller acting
// across identities. A nil Actor records an unattributed action.
type actionRequest struct {
	Identity *string          `json:"identity"`
	TaskID   int64            `json:"task_id"`
	Action   taskstate.Action `json:"action"`
	Actor    *actorPayload    `json:"actor,omitempty"`
	// Instructions and Findings answer the review gate. RetryMode is the retry's
	// worktree mode; empty means refresh_onto_base.
	Instructions string              `json:"instructions,omitempty"`
	Findings     []string            `json:"findings,omitempty"`
	RetryMode    taskstate.RetryMode `json:"retry_mode,omitempty"`
}

type actorPayload struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Principal string `json:"principal"`
}

func (p *actorPayload) actor() taskactions.Actor {
	if p == nil {
		return taskactions.Actor{}
	}
	return taskactions.Actor{
		Identity:  identity.IdentityID(p.ID),
		Kind:      identity.Kind(p.Kind),
		Principal: identity.IdentityID(p.Principal),
	}
}

// actionResponse carries the error's class beside its message. This hop
// reduces an error to a JSON string, and the dashboard chooses its HTTP
// status from the sentinel (404, 409, 503), so the class has to cross
// explicitly or every failure arrives as an opaque 500.
type actionResponse struct {
	natsrpc.Envelope
	Kind string `json:"kind,omitempty"`
}

// actionErrorKinds names the sentinels a caller matches on. Anything absent
// crosses as a message alone, which is the honest result: an unclassified
// failure is a server error at every consumer.
var actionErrorKinds = []struct {
	kind string
	err  error
}{
	{kind: "not_found", err: taskactions.ErrNotFound},
	{kind: "conflict", err: taskactions.ErrConflict},
	{kind: "stale_transition", err: storecontract.ErrStaleTransition},
	{kind: "rereview_cap", err: storecontract.ErrRereviewCapReached},
	{kind: "unavailable", err: taskactions.ErrUnavailable},
}

func actionErrorKind(err error) string {
	for _, candidate := range actionErrorKinds {
		if errors.Is(err, candidate.err) {
			return candidate.kind
		}
	}
	return ""
}

// remoteActionError restores a sentinel the daemon reported, keeping the
// daemon's own wording as the message.
type remoteActionError struct {
	message  string
	sentinel error
}

func (e remoteActionError) Error() string { return e.message }
func (e remoteActionError) Unwrap() error { return e.sentinel }

func actionErrorFor(kind string, err error) error {
	for _, candidate := range actionErrorKinds {
		if candidate.kind == kind {
			return remoteActionError{message: err.Error(), sentinel: candidate.err}
		}
	}
	return err
}

// Register serves identity-scoped task actions over NATS. Handlers use
// context.Background().
func Register(nc *nats.Conn, service taskactions.Service, log *slog.Logger) (func(), error) {
	return natsrpc.RegisterAll(nc, []natsrpc.Registration{{
		Subject: actionSubject,
		Handler: func(msg *nats.Msg) {
			var req actionRequest
			if err := json.Unmarshal(msg.Data, &req); err != nil {
				natsrpc.Respond(msg, log, "taskactions", actionResponse{Envelope: natsrpc.NewEnvelope(err)})
				return
			}
			err := service.Apply(context.Background(), req.Identity, req.Actor.actor(), req.TaskID, req.Action, taskactions.ActionPayload{
				Instructions: req.Instructions,
				Findings:     req.Findings,
				RetryMode:    req.RetryMode,
			})
			natsrpc.Respond(msg, log, "taskactions", actionResponse{
				Envelope: natsrpc.NewEnvelope(err),
				Kind:     actionErrorKind(err),
			})
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

// ApplyChatTaskAction sends an action to the daemon's responder, carrying the
// scope and the actor the caller resolved. A caller with no verified identity
// passes the zero actor, which the daemon records as unattributed.
func (c Client) ApplyChatTaskAction(ctx context.Context, scope *string, actor taskactions.Actor, id int64, action taskstate.Action, res taskactions.ActionPayload) (gateway.TaskActionResult, error) {
	if c.Conn == nil {
		return gateway.TaskActionResult{}, fmt.Errorf("task action connection is unavailable")
	}
	request := actionRequest{
		Identity:     scope,
		TaskID:       id,
		Action:       action,
		Instructions: res.Instructions,
		Findings:     res.Findings,
		RetryMode:    res.RetryMode,
		Actor: &actorPayload{
			ID:        string(actor.Identity),
			Kind:      string(actor.Kind),
			Principal: string(actor.Principal),
		},
	}
	resp, err := natsrpc.Call[actionResponse](ctx, c.rpc(), actionSubject, request)
	if err != nil {
		return gateway.TaskActionResult{}, err
	}
	if err := resp.Err(); err != nil {
		return gateway.TaskActionResult{}, actionErrorFor(resp.Kind, err)
	}
	return gateway.TaskActionResult{TaskID: id, Action: string(action), Message: fmt.Sprintf("Applied %s to task %d.", action, id)}, nil
}
