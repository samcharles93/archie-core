package gateway

import (
	"context"
	"testing"
)

// streamChat builds the local chat adapter the way the composition wires
// it, pointed at the router under test; it is the production streaming
// entry these streamed-turn tests drive.
func streamChat(r *Router) *LocalChatAdapter {
	return &LocalChatAdapter{Router: r}
}

// drainStreamed runs one streamed turn through the production chat adapter
// and returns what its stream rendered: the deltas, then the terminal
// event's kind and text.
func drainStreamed(t *testing.T, chat *LocalChatAdapter, in Inbound) ([]string, string, string, error) {
	t.Helper()
	events, err := chat.Stream(context.Background(), in)
	if err != nil {
		return nil, "", "", err
	}
	rendered, drainErr := drained(events)
	if drainErr != nil {
		return nil, "", "", drainErr
	}
	var deltas []string
	kind, text := "", ""
	for _, event := range rendered {
		switch event.Kind {
		case "delta":
			deltas = append(deltas, event.Text)
		case "done", "error":
			kind, text = event.Kind, event.Text
		}
	}
	return deltas, kind, text, nil
}

func TestLocalChatSnapshotAndStream(t *testing.T) {
	ctx := t.Context()
	sessions := NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := NewRouter(nil, nil, "web")
	router.InitSessions(sessions)
	chat := &LocalChatAdapter{Router: router, Sessions: sessions}
	snapshot, err := chat.Snapshot(ctx)
	if err != nil || snapshot.CancellationAvailable || snapshot.PersonasAvailable {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	if _, found, err := chat.GetSession(ctx, "absent"); err != nil || found {
		t.Fatalf("missing session = %v, %v", found, err)
	}
	events, err := chat.Stream(ctx, inboundFrom("browser", "web", "/help"))
	if err != nil {
		t.Fatal(err)
	}
	var got []ChatEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) < 2 || got[0].Kind != "started" || got[len(got)-1].Kind != "done" || got[len(got)-1].Text == "" {
		t.Fatalf("events = %+v", got)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := chat.Stream(cancelCtx, inbound("", "")); err == nil {
		t.Fatal("cancelled context accepted")
	}
}
