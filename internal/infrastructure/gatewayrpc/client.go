// Package gatewayrpc adapts the Gateway chat contract to gRPC.
package gatewayrpc

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

var _ gateway.ChatContract = (*Client)(nil)

type Client struct{ client pb.ChatServiceClient }

func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{client: pb.NewChatServiceClient(conn)}
}

func (c *Client) Snapshot(ctx context.Context) (gateway.ChatSnapshot, error) {
	v, err := c.client.Snapshot(ctx, &pb.SnapshotRequest{})
	if err != nil {
		return gateway.ChatSnapshot{}, err
	}
	return snapshotValue(v), nil
}

func (c *Client) GetSession(ctx context.Context, id string) (gateway.SessionContext, bool, error) {
	v, err := c.client.GetSession(ctx, &pb.GetSessionRequest{SessionId: id})
	if err != nil {
		return gateway.SessionContext{}, false, err
	}
	return sessionValue(v.Session), v.Found, nil
}

func (c *Client) RecentMessages(ctx context.Context, id string, n int) ([]gateway.Message, error) {
	v, err := c.client.RecentMessages(ctx, &pb.RecentMessagesRequest{SessionId: id, Limit: int64(n)})
	if err != nil {
		return nil, err
	}
	return mapValues(v.Messages, messageValue), nil
}

func (c *Client) RecentTurns(ctx context.Context, id string, n int) ([]gateway.TurnRecord, error) {
	v, err := c.client.RecentTurns(ctx, &pb.RecentTurnsRequest{SessionId: id, Limit: int64(n)})
	if err != nil {
		return nil, err
	}
	return mapValues(v.Turns, turnValue), nil
}

func (c *Client) Route(ctx context.Context, msg gateway.Message) (gateway.ChatReply, error) {
	v, err := c.client.Route(ctx, &pb.RouteRequest{Message: messageProto(msg)})
	if err != nil {
		return gateway.ChatReply{}, err
	}
	return gateway.ChatReply{Text: v.Text, SessionID: v.SessionId}, nil
}

func (c *Client) Cancel(ctx context.Context, id string) (gateway.ChatCancellation, error) {
	v, err := c.client.Cancel(ctx, &pb.CancelRequest{SessionId: id})
	if err != nil {
		return gateway.ChatCancellation{}, err
	}
	return gateway.ChatCancellation{Cancelled: v.Cancelled, Dropped: int(v.Dropped)}, nil
}

func (c *Client) SetPersona(ctx context.Context, id, name string) (bool, error) {
	v, err := c.client.SetPersona(ctx, &pb.SetPersonaRequest{SessionId: id, Name: name})
	if err != nil {
		return false, err
	}
	return v.Found, nil
}

func (c *Client) ApplyTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action) (gateway.TaskActionResult, error) {
	v, err := c.client.ApplyTaskAction(ctx, &pb.ApplyTaskActionRequest{Identity: identity, TaskId: taskID, Action: string(action)})
	if err != nil {
		return gateway.TaskActionResult{}, err
	}
	return gateway.TaskActionResult{TaskID: v.TaskId, Action: v.Action, Message: v.Message}, nil
}

func (c *Client) Stream(ctx context.Context, msg gateway.Message) (<-chan gateway.ChatEvent, error) {
	stream, err := c.client.Stream(ctx, &pb.StreamRequest{Message: messageProto(msg)})
	if err != nil {
		return nil, err
	}
	out := make(chan gateway.ChatEvent)
	go func() {
		defer close(out)
		for {
			v, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			var event gateway.ChatEvent
			if err != nil {
				event = gateway.ChatEvent{Kind: "error", Text: err.Error()}
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
