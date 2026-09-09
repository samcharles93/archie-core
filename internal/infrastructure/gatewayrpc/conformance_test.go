package gatewayrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
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

// recordingTaskActor captures the identity scope an action reached the daemon
// with. A pointer, because nil is a distinct authorization, not a blank name.
type recordingTaskActor struct {
	identity *string
	taskID   int64
	action   taskstate.Action
}

func (a *recordingTaskActor) ApplyChatTaskAction(
	_ context.Context, identity *string, taskID int64, action taskstate.Action,
) (gateway.TaskActionResult, error) {
	a.identity, a.taskID, a.action = identity, taskID, action
	return gateway.TaskActionResult{TaskID: taskID, Action: string(action), Message: "applied"}, nil
}

// TestTaskActionScopeSurvivesTheWire pins the distinction the daemon's action
// service draws (internal/domain/taskactions.Service.Apply): a nil identity is
// an authenticated dashboard operator acting across identities, a non-nil one
// is a chat user acting on their own task. ApplyTaskAction cannot express the
// operator case -- "" is a real identity in a single-identity deployment (see
// chatTaskProfiles) -- so it has its own contract method, and the difference
// has to survive both the local adapter and the gRPC hop.
func TestTaskActionScopeSurvivesTheWire(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			actor := &recordingTaskActor{}
			local := &gateway.LocalChatAdapter{TaskActor: actor}
			var chat gateway.ChatContract = local
			if mode == "grpc" {
				chat = remoteChat(t, local)
			}

			result, err := chat.ApplyOperatorTaskAction(ctx, 42, taskstate.ActionApprove)
			if err != nil {
				t.Fatalf("operator action: %v", err)
			}
			if actor.identity != nil {
				t.Fatalf("operator action arrived scoped to %q, want an unscoped nil identity", *actor.identity)
			}
			if actor.taskID != 42 || actor.action != taskstate.ActionApprove {
				t.Fatalf("operator action = (%d, %q), want (42, %q)", actor.taskID, actor.action, taskstate.ActionApprove)
			}
			if result.TaskID != 42 || result.Action != string(taskstate.ActionApprove) || result.Message != "applied" {
				t.Fatalf("operator result = %+v, want the daemon's result", result)
			}

			if _, err := chat.ApplyTaskAction(ctx, "scout", 7, taskstate.ActionRetry); err != nil {
				t.Fatalf("chat action: %v", err)
			}
			if actor.identity == nil || *actor.identity != "scout" {
				t.Fatalf("chat action lost its identity scope: %v", actor.identity)
			}
		})
	}
}

// failingTaskActor reports one error, whatever the action.
type failingTaskActor struct{ err error }

func (a failingTaskActor) ApplyChatTaskAction(
	context.Context, *string, int64, taskstate.Action,
) (gateway.TaskActionResult, error) {
	return gateway.TaskActionResult{}, a.err
}

// TestTaskActionErrorsKeepTheirSentinelOverGRPC: the dashboard answers 404,
// 409 or 503 by matching these sentinels, and gRPC carries a code and a
// string, not a Go error. Losing the class here turns "that task already
// moved on" into "the daemon is broken".
func TestTaskActionErrorsKeepTheirSentinelOverGRPC(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: fmt.Errorf("task 7: %w", taskactions.ErrNotFound)},
		{name: "conflict", err: fmt.Errorf("%w: action \"approve\" is not available while task is parked", taskactions.ErrConflict)},
		{name: "stale transition", err: fmt.Errorf("stop task 7: %w", store.ErrStaleTransition)},
		{name: "unavailable", err: taskactions.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			local := &gateway.LocalChatAdapter{TaskActor: failingTaskActor{err: tc.err}}
			chat := remoteChat(t, local)

			_, err := chat.ApplyOperatorTaskAction(ctx, 7, taskstate.ActionApprove)
			if err == nil {
				t.Fatalf("ApplyOperatorTaskAction error = nil, want %v", tc.err)
			}
			if !errors.Is(err, errors.Unwrap(tc.err)) && !errors.Is(err, tc.err) {
				t.Fatalf("error %v is not the daemon's sentinel %v", err, tc.err)
			}
			if !strings.Contains(err.Error(), tc.err.Error()) {
				t.Fatalf("error message = %q, want it to carry the daemon's %q", err.Error(), tc.err.Error())
			}
		})
	}
}
