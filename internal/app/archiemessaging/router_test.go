package archiemessaging

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type mockChatContract struct {
	messaging.ChatContract
	routeFn  func(ctx context.Context, in messaging.Inbound) (messaging.ChatReply, error)
	streamFn func(ctx context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error)
	actionFn func(ctx context.Context, identity string, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error)
}

func (m *mockChatContract) Route(ctx context.Context, in messaging.Inbound) (messaging.ChatReply, error) {
	if m.routeFn != nil {
		return m.routeFn(ctx, in)
	}
	return messaging.ChatReply{Text: "mock reply", SessionID: "s1"}, nil
}

func (m *mockChatContract) Stream(ctx context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, in)
	}
	ch := make(chan messaging.ChatEvent, 3)
	ch <- messaging.ChatEvent{Kind: "delta", Text: "hello "}
	ch <- messaging.ChatEvent{Kind: "delta", Text: "world"}
	ch <- messaging.ChatEvent{Kind: "done", Text: "hello world"}
	close(ch)
	return ch, nil
}

func (m *mockChatContract) ApplyTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error) {
	if m.actionFn != nil {
		return m.actionFn(ctx, identity, taskID, action)
	}
	return messaging.TaskActionResult{TaskID: taskID, Action: string(action), Message: "ok"}, nil
}

type mockTurnStream struct {
	deltas []string
	tools  []messaging.ToolCallEvent
	media  []messaging.MediaEvent
}

func (s *mockTurnStream) Delta(text string) {
	s.deltas = append(s.deltas, text)
}

func (s *mockTurnStream) ToolCall(ev messaging.ToolCallEvent) {
	s.tools = append(s.tools, ev)
}

func (s *mockTurnStream) Media(ev messaging.MediaEvent) {
	s.media = append(s.media, ev)
}

func TestContractRouterRoute(t *testing.T) {
	mock := &mockChatContract{
		routeFn: func(ctx context.Context, in messaging.Inbound) (messaging.ChatReply, error) {
			if in.Message.Text != "ping" {
				t.Errorf("got text %q, want 'ping'", in.Message.Text)
			}
			return messaging.ChatReply{Text: "pong", SessionID: "s-1"}, nil
		},
	}
	router := NewContractRouter(mock, "test-channel")

	reply, err := router.Route(context.Background(), gateway.Inbound{
		Message: messaging.Message{Text: "ping"},
	})
	if err != nil {
		t.Fatalf("router.Route: %v", err)
	}
	if reply != "pong" {
		t.Errorf("reply = %q, want 'pong'", reply)
	}
}

func TestContractRouterRouteStream(t *testing.T) {
	mock := &mockChatContract{}
	router := NewContractRouter(mock, "test-channel")

	sink := &mockTurnStream{}
	full, err := router.RouteStream(context.Background(), gateway.Inbound{
		Message: messaging.Message{Text: "generate"},
	}, sink)
	if err != nil {
		t.Fatalf("router.RouteStream: %v", err)
	}
	if full != "hello world" {
		t.Errorf("full text = %q, want 'hello world'", full)
	}
	if len(sink.deltas) != 2 || sink.deltas[0] != "hello " || sink.deltas[1] != "world" {
		t.Errorf("sink deltas = %v, want ['hello ', 'world']", sink.deltas)
	}
}

func TestContractRouterTaskCancel(t *testing.T) {
	var capturedAction taskstate.Action
	var capturedTaskID int64
	mock := &mockChatContract{
		actionFn: func(ctx context.Context, identity string, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error) {
			capturedAction = action
			capturedTaskID = taskID
			return messaging.TaskActionResult{TaskID: taskID, Action: string(action), Message: "cancelled"}, nil
		},
	}
	router := NewContractRouter(mock, "test-channel")

	err := router.Controller.Cancel(context.Background(), 42, "bot1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if capturedTaskID != 42 || capturedAction != taskstate.ActionCancel {
		t.Errorf("captured taskID=%d action=%s, want 42 and cancel", capturedTaskID, capturedAction)
	}
}
