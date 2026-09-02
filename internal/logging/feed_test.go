package logging

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestFeedHandlerWithAttrsPropagatesToWrappedHandler(t *testing.T) {
	feed := NewFeed(2)
	var buf bytes.Buffer
	logger := slog.New(NewFeedHandler(slog.NewJSONHandler(&buf, nil), feed)).With("component", "curator")
	logger.Info("ran")

	entries := feed.Snapshot()
	if len(entries) != 1 || entries[0].Fields["component"] != "curator" {
		t.Fatalf("feed entry = %#v, want component=curator", entries)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(persistent sink output) = %v", err)
	}
	if decoded["component"] != "curator" {
		t.Fatalf("persistent sink output = %#v, want component=curator", decoded)
	}
}

type testWriter struct{}

func (testWriter) Write(p []byte) (int, error) { return len(p), nil }
