// Package gatewayrpc adapts the Gateway chat contract to gRPC.
package gatewayrpc

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

var _ messaging.ChatContract = (*Client)(nil)

// Client implements messaging.ChatContract over the chat service. The proto
// is unchanged by the messaging migration, so history crosses without its
// role and is re-addressed from the owning session on arrival; StoreClient
// serves the SessionStore view over the same RPCs.
type Client struct{ client pb.ChatServiceClient }

func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{client: pb.NewChatServiceClient(conn)}
}

func (c *Client) Snapshot(ctx context.Context) (messaging.ChatSnapshot, error) {
	v, err := c.client.Snapshot(ctx, &pb.SnapshotRequest{})
	if err != nil {
		return messaging.ChatSnapshot{}, err
	}
	return snapshotValue(v), nil
}

func (c *Client) GetSession(ctx context.Context, id string) (messaging.SessionContext, bool, error) {
	v, err := c.client.GetSession(ctx, &pb.GetSessionRequest{SessionId: id})
	if err != nil {
		return messaging.SessionContext{}, false, err
	}
	return sessionValue(v.Session), v.Found, nil
}

func (c *Client) RecentMessages(ctx context.Context, id string, n int) ([]messaging.Message, error) {
	v, err := c.client.RecentMessages(ctx, &pb.RecentMessagesRequest{SessionId: id, Limit: int64(n)})
	if err != nil {
		return nil, err
	}
	return addressRecords(ctx, c.client, id, v.Messages)
}

func (c *Client) RecentTurns(ctx context.Context, id string, n int) ([]messaging.TurnRecord, error) {
	v, err := c.client.RecentTurns(ctx, &pb.RecentTurnsRequest{SessionId: id, Limit: int64(n)})
	if err != nil {
		return nil, err
	}
	return mapValues(v.Turns, turnValue), nil
}

func (c *Client) Route(ctx context.Context, in messaging.Inbound) (messaging.ChatReply, error) {
	v, err := c.client.Route(ctx, &pb.RouteRequest{Message: inboundProto(in)})
	if err != nil {
		return messaging.ChatReply{}, err
	}
	return messaging.ChatReply{Text: v.Text, SessionID: v.SessionId}, nil
}

func (c *Client) Cancel(ctx context.Context, id string) (messaging.ChatCancellation, error) {
	v, err := c.client.Cancel(ctx, &pb.CancelRequest{SessionId: id})
	if err != nil {
		return messaging.ChatCancellation{}, err
	}
	return messaging.ChatCancellation{Cancelled: v.Cancelled, Dropped: int(v.Dropped)}, nil
}

func (c *Client) SetPersona(ctx context.Context, id, name string) (bool, error) {
	v, err := c.client.SetPersona(ctx, &pb.SetPersonaRequest{SessionId: id, Name: name})
	if err != nil {
		return false, err
	}
	return v.Found, nil
}

func (c *Client) ApplyTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error) {
	v, err := c.client.ApplyTaskAction(ctx, &pb.ApplyTaskActionRequest{Identity: identity, TaskId: taskID, Action: string(action)})
	if err != nil {
		return messaging.TaskActionResult{}, taskActionError(err)
	}
	return messaging.TaskActionResult{TaskID: v.TaskId, Action: v.Action, Message: v.Message}, nil
}

func (c *Client) ApplyOperatorTaskAction(ctx context.Context, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error) {
	v, err := c.client.ApplyOperatorTaskAction(ctx, &pb.ApplyOperatorTaskActionRequest{TaskId: taskID, Action: string(action)})
	if err != nil {
		return messaging.TaskActionResult{}, taskActionError(err)
	}
	return messaging.TaskActionResult{TaskID: v.TaskId, Action: v.Action, Message: v.Message}, nil
}

func (c *Client) Stream(ctx context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	stream, err := c.client.Stream(ctx, &pb.StreamRequest{Message: inboundProto(in)})
	if err != nil {
		return nil, err
	}
	out := make(chan messaging.ChatEvent)
	go func() {
		defer close(out)
		for {
			v, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			var event messaging.ChatEvent
			if err != nil {
				event = messaging.ChatEvent{Kind: "error", Text: err.Error()}
			} else {
				event = eventValue(v)
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return out, nil
}
