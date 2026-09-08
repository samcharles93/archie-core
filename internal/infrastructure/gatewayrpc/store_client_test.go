package gatewayrpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// remoteStore wires a StoreClient to a real SQLite session store through the
// gRPC server, mirroring production (standalone gateway owns the store,
// archied dials in).
func remoteStore(t *testing.T, sessions gateway.SessionStore, chat gateway.ChatContract) gateway.SessionStore {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, chat, sessions)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///gateway", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewStoreClient(conn)
}

// TestStoreClientPreservesCanonicalRecords pins the remote store contract:
// messages cross the wire in their gateway shape (the proto is unchanged)
// and come back as the same canonical records -- sender, derived role, and
// conversation address intact. Roles are passed empty to prove the server
// derives them from the owning session, exactly like the in-process
// boundary.
func TestStoreClientPreservesCanonicalRecords(t *testing.T) {
	ctx := t.Context()
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(nil, nil, "web")
	router.InitSessions(sessions)
	local := &gateway.LocalChatAdapter{Router: router, Sessions: sessions}
	store := remoteStore(t, sessions, local)

	sc := gateway.SessionContext{
		SessionID:    "sess-1",
		Source:       gateway.SessionSource{Platform: "web", BotUser: "archie", ChannelID: "chat-1"},
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
	}
	if err := store.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}

	msgs := []messaging.Message{
		{SourceID: "w-1", Sender: "web", Text: "hello", At: time.Now().UTC()},
		{SourceID: "w-2", Sender: "archie", Text: "hi", At: time.Now().UTC()},
	}
	for _, m := range msgs {
		if err := store.SaveMessage(ctx, "sess-1", m); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	got, err := store.RecentMessages(ctx, "sess-1", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("RecentMessages returned %d messages, want 2", len(got))
	}
	want := []messaging.Message{
		{
			SourceID: "w-1", Sender: "web", Role: messaging.RoleUser, Text: "hello",
			ConversationID: messaging.ConversationID{ChannelID: "chat-1"},
		},
		{
			SourceID: "w-2", Sender: "archie", Role: messaging.RoleAssistant, Text: "hi",
			ConversationID: messaging.ConversationID{ChannelID: "chat-1"},
		},
	}
	for i, w := range want {
		if got[i].SourceID != w.SourceID || got[i].Sender != w.Sender ||
			got[i].Role != w.Role || got[i].Text != w.Text ||
			got[i].ConversationID != w.ConversationID {
			t.Fatalf("message %d = %+v, want %+v", i, got[i], w)
		}
		if got[i].ID == "" {
			t.Fatalf("message %d has no canonical ID", i)
		}
	}

	page, err := store.SearchMessages(ctx, "sess-1", gateway.MessageQuery{Query: "hello", Limit: 10})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Role != messaging.RoleUser ||
		page.Messages[0].ConversationID.ChannelID != "chat-1" {
		t.Fatalf("SearchMessages = %+v, want the user message with its conversation", page.Messages)
	}
}
