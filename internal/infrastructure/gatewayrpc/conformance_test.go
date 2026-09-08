package gatewayrpc

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

func remoteChat(t *testing.T, local gateway.ChatContract) gateway.ChatContract {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, local)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///gateway", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewClient(conn)
}

func TestChatContractConformance(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			sessions := gateway.NewSessionStoreMemory()
			t.Cleanup(func() { _ = sessions.Close() })
			router := gateway.NewRouter(nil, nil, "web")
			router.InitSessions(sessions)
			local := &gateway.LocalChatAdapter{Router: router, Sessions: sessions}
			var chat gateway.ChatContract = local
			if mode == "grpc" {
				chat = remoteChat(t, local)
			}
			if _, found, err := chat.GetSession(ctx, "missing"); err != nil || found {
				t.Fatalf("missing session: %v %v", found, err)
			}
			msg := gateway.Inbound{
				Message: messaging.Message{ConversationID: messaging.ConversationID{ChannelID: "browser"}, Sender: "web", Role: messaging.RoleUser, Text: "/help"},
				Page:    "/tasks",
			}
			reply, err := chat.Route(ctx, msg)
			if err != nil || reply.Text == "" || reply.SessionID == "" {
				t.Fatalf("route: %+v %v", reply, err)
			}
			session, found, err := chat.GetSession(ctx, reply.SessionID)
			if err != nil || !found || session.Source.Platform != "web" {
				t.Fatalf("session: %+v %v %v", session, found, err)
			}
			snap, err := chat.Snapshot(ctx)
			if err != nil || len(snap.Sessions) != 1 || snap.CancellationAvailable || snap.PersonasAvailable {
				t.Fatalf("snapshot: %+v %v", snap, err)
			}
			if _, err := chat.RecentMessages(ctx, reply.SessionID, 200); err != nil {
				t.Fatal(err)
			}
			if turns, err := chat.RecentTurns(ctx, reply.SessionID, 200); err != nil || len(turns) != 0 {
				t.Fatalf("turns: %+v %v", turns, err)
			}
			if _, err := chat.Cancel(ctx, reply.SessionID); err == nil {
				t.Fatal("missing cancellation capability accepted")
			}
			if _, err := chat.SetPersona(ctx, reply.SessionID, "missing"); err == nil {
				t.Fatal("missing persona capability accepted")
			}
			events, err := chat.Stream(ctx, msg)
			if err != nil {
				t.Fatal(err)
			}
			var got []gateway.ChatEvent
			for event := range events {
				got = append(got, event)
			}
			if len(got) < 2 || got[0].Kind != "started" || got[len(got)-1].Kind != "done" || got[len(got)-1].Text != reply.Text {
				t.Fatalf("stream: %+v", got)
			}
		})
	}
}

func TestWireValuesPreserveHistoryAndMedia(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 2, 3, 456, time.UTC)
	tool := gateway.ToolCallEvent{ID: "tool-1", Name: "read", Parameters: "{}", Output: "result", Err: "failed"}
	turn := gateway.TurnRecord{TurnID: "turn", SessionID: "session", SourceID: "source", Status: gateway.TurnStatusPartial, Attempt: 3, OwnerID: "owner", InputMessageID: "input", AssistantMessageID: "assistant", PartialText: "partial", ResponseText: "reply", ToolCalls: []gateway.ToolCallEvent{tool}, Error: "failure", CreatedAt: now, UpdatedAt: now}
	if got := turnValue(turnProto(turn)); !reflect.DeepEqual(got, turn) {
		t.Fatalf("turn round trip: %+v", got)
	}
	size := int64(0)
	width := 640
	height := 480
	duration := 3
	event := gateway.ChatEvent{Kind: "media", Text: "caption", SessionID: "session", Tool: tool, Media: gateway.MediaEvent{ToolName: "send_file", Attachment: gateway.MediaAttachment{Type: "video", FileID: "file", URL: "https://example.test/video", Path: "/tmp/video", MIMEType: "video/mp4", FileName: "video.mp4", FileSize: &size, Width: &width, Height: &height, Duration: &duration}}}
	if got := eventValue(eventProto(event)); !reflect.DeepEqual(got, event) {
		t.Fatalf("event round trip: %+v", got)
	}
	if got := inboundValue(inboundProto(gateway.Inbound{})); !got.Message.At.IsZero() {
		t.Fatalf("zero time became %v", got.Message.At)
	}
}
