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

// Identity is a pointer because its absence is meaningful: nil is a
// caller authenticated across identities, which the daemon's service
// distinguishes from any named identity, empty included. It is a SCOPE -- which
// tasks the caller may touch -- and never an actor.
//
// Actor carries who performed the action and whose authority permitted it, as
// resolved by the caller that verified the credential. Its absence is meaningful:
// an absent actor records an unattributed action, never a human's.
type actionRequest struct {
	Identity *string          `json:"identity"`
	TaskID   int64            `json:"task_id"`
	Action   taskstate.Action `json:"action"`
	Actor    *actorPayload    `json:"actor,omitempty"`
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
			err := service.Apply(context.Background(), req.Identity, req.Actor.actor(), req.TaskID, req.Action)
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
func (c Client) ApplyChatTaskAction(ctx context.Context, scope *string, actor taskactions.Actor, id int64, action taskstate.Action) (gateway.TaskActionResult, error) {
	if c.Conn == nil {
		return gateway.TaskActionResult{}, fmt.Errorf("task action connection is unavailable")
	}
	request := actionRequest{
		Identity: scope,
		TaskID:   id,
		Action:   action,
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
