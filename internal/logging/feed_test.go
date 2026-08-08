package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestFeedCapturesStructuredSlogEntriesInOrder(t *testing.T) {
	feed := NewFeed(2)
	logger := slog.New(NewFeedHandler(slog.NewJSONHandler(testWriter{}, nil), feed))
	logger.Info("started", "component", "daemon")
	logger.Warn("slow", "component", "worker")

	entries := feed.Snapshot()
	if len(entries) != 2 || entries[0].ID == 0 || entries[1].ID <= entries[0].ID {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].Message != "started" || entries[0].Fields["component"] != "daemon" {
		t.Fatalf("first entry = %#v", entries[0])
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	updates := feed.Subscribe(ctx)
	logger.Error("failed", "component", "worker")
	if got := <-updates; got.Message != "failed" || got.Level != "ERROR" {
		t.Fatalf("live entry = %#v", got)
	}
}

type testWriter struct{}

func (testWriter) Write(p []byte) (int, error) { return len(p), nil }
