package gateway

import (
	"context"
	"testing"
)

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
	events, err := chat.Stream(ctx, Message{ChannelID: "browser", From: "web", Text: "/help"})
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
	if _, err := chat.Stream(cancelCtx, Message{}); err == nil {
		t.Fatal("cancelled context accepted")
	}
}
