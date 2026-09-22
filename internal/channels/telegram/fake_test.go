package telegram

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type fakeChatContract struct {
	snapshot       messaging.ChatSnapshot
	snapshotErr    error
	routeFunc      func(ctx context.Context, in messaging.Inbound) (messaging.ChatReply, error)
	streamFunc     func(ctx context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error)
	cancelFunc     func(ctx context.Context, id string) (messaging.ChatCancellation, error)
	setPersonaFunc func(ctx context.Context, id, name string) (bool, error)
	details        map[string]messaging.ModelDetails
}

var (
	_ messaging.ChatContract         = (*fakeChatContract)(nil)
	_ messaging.DetailedModelManager = (*fakeChatContract)(nil)
)

func (f *fakeChatContract) ModelDetails(ref string) (messaging.ModelDetails, bool) {
	if f.details == nil {
		return messaging.ModelDetails{}, false
	}
	d, ok := f.details[ref]
	return d, ok
}

func (f *fakeChatContract) Snapshot(ctx context.Context) (messaging.ChatSnapshot, error) {
	if f.snapshotErr != nil {
		return messaging.ChatSnapshot{}, f.snapshotErr
	}
	return f.snapshot, nil
}

func (f *fakeChatContract) GetSession(ctx context.Context, id string) (messaging.SessionContext, bool, error) {
	return messaging.SessionContext{}, false, nil
}

func (f *fakeChatContract) RecentMessages(ctx context.Context, id string, n int) ([]messaging.Message, error) {
	return nil, nil
}

func (f *fakeChatContract) RecentTurns(ctx context.Context, id string, n int) ([]messaging.TurnRecord, error) {
	return nil, nil
}

func (f *fakeChatContract) Route(ctx context.Context, in messaging.Inbound) (messaging.ChatReply, error) {
	if f.routeFunc != nil {
		return f.routeFunc(ctx, in)
	}
	return messaging.ChatReply{Text: "ok"}, nil
}

func (f *fakeChatContract) Stream(ctx context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	if f.streamFunc != nil {
		return f.streamFunc(ctx, in)
	}
	ch := make(chan messaging.ChatEvent, 2)
	ch <- messaging.ChatEvent{Kind: "started", SessionID: "s1"}
	ch <- messaging.ChatEvent{Kind: "done", Text: "done", SessionID: "s1"}
	close(ch)
	return ch, nil
}

func (f *fakeChatContract) Cancel(ctx context.Context, id string) (messaging.ChatCancellation, error) {
	if f.cancelFunc != nil {
		return f.cancelFunc(ctx, id)
	}
	return messaging.ChatCancellation{Cancelled: true}, nil
}

func (f *fakeChatContract) SetPersona(ctx context.Context, id, name string) (bool, error) {
	if f.setPersonaFunc != nil {
		return f.setPersonaFunc(ctx, id, name)
	}
	return true, nil
}

func (f *fakeChatContract) ApplyTaskAction(ctx context.Context, identity string, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error) {
	return messaging.TaskActionResult{}, nil
}

func (f *fakeChatContract) ApplyOperatorTaskAction(ctx context.Context, actor taskactions.Actor, taskID int64, action taskstate.Action) (messaging.TaskActionResult, error) {
	return messaging.TaskActionResult{}, nil
}
