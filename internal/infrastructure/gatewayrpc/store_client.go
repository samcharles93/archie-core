package gatewayrpc

import (
	"context"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

var _ gateway.SessionStore = (*StoreClient)(nil)

// StoreClient implements gateway.SessionStore over the chat service's
// session-store RPCs. It is separate from Client (the ChatContract view)
// because the two contracts serve different reads over the same messages.
//
// Role does not cross the wire (the proto is unchanged by the messaging
// migration). Both sides derive it from the owning session instead -- the
// server on write, this client on read -- so they agree without a proto
// change.
type StoreClient struct{ client pb.ChatServiceClient }

func NewStoreClient(conn grpc.ClientConnInterface) *StoreClient {
	return &StoreClient{client: pb.NewChatServiceClient(conn)}
}

func (c *StoreClient) Get(ctx context.Context, id string) (*gateway.SessionContext, error) {
	v, err := c.client.GetSession(ctx, &pb.GetSessionRequest{SessionId: id})
	if err != nil {
		return nil, err
	}
	if !v.Found {
		return nil, nil
	}
	s := sessionValue(v.Session)
	return &s, nil
}

func (c *StoreClient) Save(ctx context.Context, s gateway.SessionContext) error {
	_, e := c.client.SaveSession(ctx, &pb.SaveSessionRequest{Session: sessionProto(s)})
	return e
}

func (c *StoreClient) GetByChannel(ctx context.Context, p, ch string) ([]gateway.SessionContext, error) {
	v, e := c.client.GetSessionsByChannel(ctx, &pb.GetSessionsByChannelRequest{Platform: p, ChannelId: ch})
	if e != nil {
		return nil, e
	}
	return mapValues(v.Sessions, sessionValue), nil
}

func (c *StoreClient) Delete(ctx context.Context, id string) error {
	_, e := c.client.DeleteSession(ctx, &pb.DeleteSessionRequest{SessionId: id})
	return e
}

func (c *StoreClient) Touch(ctx context.Context, id string) error {
	_, e := c.client.TouchSession(ctx, &pb.TouchSessionRequest{SessionId: id})
	return e
}

func (c *StoreClient) List(ctx context.Context) ([]gateway.SessionContext, error) {
	v, e := c.client.ListSessions(ctx, &pb.ListSessionsRequest{})
	if e != nil {
		return nil, e
	}
	return mapValues(v.Sessions, sessionValue), nil
}

func (c *StoreClient) SaveMessage(ctx context.Context, id string, m messaging.Message) error {
	_, e := c.client.SaveMessage(ctx, &pb.SaveMessageRequest{SessionId: id, Message: storedProto(m)})
	return e
}

func (c *StoreClient) DeleteRecentMessages(ctx context.Context, id string, n int) (int, error) {
	v, e := c.client.DeleteRecentMessages(ctx, &pb.DeleteRecentMessagesRequest{SessionId: id, Limit: int64(n)})
	return int(v.Deleted), e
}

func (c *StoreClient) MessageCount(ctx context.Context, id string) (int, error) {
	v, e := c.client.MessageCount(ctx, &pb.MessageCountRequest{SessionId: id})
	return int(v.Count), e
}

func (c *StoreClient) SaveMessages(ctx context.Context, id string, m []messaging.Message) error {
	_, e := c.client.SaveMessages(ctx, &pb.SaveMessagesRequest{SessionId: id, Messages: mapValues(m, storedProto)})
	return e
}

func (c *StoreClient) ReplaceMessages(ctx context.Context, id string, m []messaging.Message, s []string) error {
	_, e := c.client.ReplaceMessages(ctx, &pb.ReplaceMessagesRequest{SessionId: id, Messages: mapValues(m, storedProto), Superseded: s})
	return e
}

func (c *StoreClient) RecentMessages(ctx context.Context, id string, n int) ([]messaging.Message, error) {
	// No store-RecentMessages RPC exists; the chat contract serves the same
	// history in-process and over the wire. Re-address it (see
	// addressRecords).
	v, err := c.client.RecentMessages(ctx, &pb.RecentMessagesRequest{SessionId: id, Limit: int64(n)})
	if err != nil {
		return nil, err
	}
	return addressRecords(ctx, c.client, id, v.Messages)
}

func (c *StoreClient) SearchMessages(ctx context.Context, id string, q gateway.MessageQuery) (gateway.MessagePage, error) {
	v, e := c.client.SearchMessages(ctx, &pb.SearchMessagesRequest{SessionId: id, Query: q.Query, Limit: int64(q.Limit), Offset: int64(q.Offset)})
	if e != nil {
		return gateway.MessagePage{}, e
	}
	msgs, err := addressRecords(ctx, c.client, id, v.Messages)
	if err != nil {
		return gateway.MessagePage{}, err
	}
	return gateway.MessagePage{Messages: msgs, NextOffset: int(v.NextOffset), HasMore: v.HasMore, Truncated: v.Truncated}, nil
}

func (c *StoreClient) Close() error { return nil }

// addressRecords restores the canonical fields the wire drops -- role and
// conversation address -- from the owning session, which is where the
// server derived them from on the way out. A missing session yields zero
// values, matching the store's LEFT JOIN for orphaned rows.
func addressRecords(ctx context.Context, client pb.ChatServiceClient, id string, msgs []*pb.Message) ([]messaging.Message, error) {
	var botUser string
	var conv messaging.ConversationID
	v, err := client.GetSession(ctx, &pb.GetSessionRequest{SessionId: id})
	if err != nil {
		return nil, err
	}
	if v.Found {
		sc := sessionValue(v.Session)
		botUser = sc.Source.BotUser
		conv = messaging.ConversationID{ChannelID: sc.Source.ChannelID, ThreadID: sc.Source.ThreadID}
	}
	out := make([]messaging.Message, 0, len(msgs))
	for _, m := range msgs {
		stored := storedValue(m)
		stored.ConversationID = conv
		stored.Role = gateway.RoleForSender(stored.Sender, botUser)
		out = append(out, stored)
	}
	return out, nil
}
