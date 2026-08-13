package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/logging"
)

// fakePublisher is a LogPublisher that records every call and can be told to
// fail, so tests can assert both the happy path and the "must never block or
// fail the run" requirement.
type fakePublisher struct {
	mu       sync.Mutex
	subjects []string
	payloads [][]byte
	failWith error
}

func (f *fakePublisher) Publish(subject string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects = append(f.subjects, subject)
	f.payloads = append(f.payloads, append([]byte(nil), data...))
	return f.failWith
}

func (f *fakePublisher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.payloads)
}

// capturingHandler is a minimal slog.Handler that records every record it
// receives, standing in for the agentworker's wrapped process stderr handler.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.records))
	for i, r := range h.records {
		out[i] = r.Message
	}
	return out
}

func TestSystemLogHandlerPublishesToTheTasksSystemSubject(t *testing.T) {
	pub := &fakePublisher{}
	next := &capturingHandler{}
	handler := NewSystemLogHandler(next, pub, 42)
	logger := slog.New(handler)

	logger.Warn("gate failed", "component", "gate")

	if got, want := pub.calls(), 1; got != want {
		t.Fatalf("publish calls = %d, want %d", got, want)
	}
	if got, want := pub.subjects[0], SubjectForSystem(42); got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}

	var entry logging.Entry
	if err := json.Unmarshal(pub.payloads[0], &entry); err != nil {
		t.Fatalf("payload is not a logging.Entry: %v", err)
	}
	if entry.Message != "gate failed" {
		t.Errorf("Message = %q, want %q", entry.Message, "gate failed")
	}
	if entry.Level != "WARN" {
		t.Errorf("Level = %q, want WARN", entry.Level)
	}
	if entry.Fields["component"] != "gate" {
		t.Errorf("Fields[component] = %v, want gate", entry.Fields["component"])
	}
}

func TestSystemLogHandlerAlwaysTeesToNextRegardlessOfPublishOutcome(t *testing.T) {
	tests := []struct {
		name     string
		failWith error
	}{
		{name: "publish succeeds"},
		{name: "publish fails", failWith: errors.New("nats: no responders")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := &fakePublisher{failWith: tt.failWith}
			next := &capturingHandler{}
			logger := slog.New(NewSystemLogHandler(next, pub, 1))

			logger.Info("still visible on stderr")

			// A failed publish also emits its own one-time warning onto
			// next (see TestSystemLogHandlerLogsPublishFailureOnceNotPerRecord),
			// so check membership, not position.
			found := false
			for _, msg := range next.messages() {
				found = found || msg == "still visible on stderr"
			}
			if !found {
				t.Errorf("next handler messages = %v, want %q present regardless of publish outcome",
					next.messages(), "still visible on stderr")
			}
		})
	}
}

// TestSystemLogHandlerNeverFailsTheRunOnPublishError guards the explicit
// design requirement: log shipping must never block or fail the run it is
// reporting on. A slog.Logger call that returned an error here would
// propagate nowhere useful (slog callers never check it), but Handle itself
// must not return a publish error either, or a handler chain that does check
// (as ours might grow to) would start treating a dead NATS connection as a
// logging failure worth surfacing to the caller.
func TestSystemLogHandlerNeverFailsTheRunOnPublishError(t *testing.T) {
	pub := &fakePublisher{failWith: errors.New("nats: no responders")}
	handler := NewSystemLogHandler(&capturingHandler{}, pub, 1)

	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	if err := handler.Handle(context.Background(), rec); err != nil {
		t.Errorf("Handle() = %v, want nil even when the publisher errors", err)
	}
}

// TestSystemLogHandlerLogsPublishFailureOnceNotPerRecord guards against a
// dead NATS connection turning every subsequent log line into a second
// failure notice on stderr, which would drown out the very output the
// operator is trying to read.
func TestSystemLogHandlerLogsPublishFailureOnceNotPerRecord(t *testing.T) {
	pub := &fakePublisher{failWith: errors.New("nats: no responders")}
	next := &capturingHandler{}
	logger := slog.New(NewSystemLogHandler(next, pub, 1))

	for range 5 {
		logger.Info("line")
	}

	failureNotices := 0
	for _, msg := range next.messages() {
		if msg != "line" {
			failureNotices++
		}
	}
	if failureNotices != 1 {
		t.Errorf("failure notices on next = %d, want exactly 1 across 5 failed publishes", failureNotices)
	}
}

// TestSystemLogHandlerMarshalFailureWarnsOnceAndNeverPublishes guards the
// other delivery failure mode alongside a rejected Publish: an attr value
// json.Marshal can't encode (e.g. a channel) must not silently vanish with
// zero signal, and must not reach the publisher with malformed data either.
func TestSystemLogHandlerMarshalFailureWarnsOnceAndNeverPublishes(t *testing.T) {
	pub := &fakePublisher{}
	next := &capturingHandler{}
	logger := slog.New(NewSystemLogHandler(next, pub, 1))

	logger.Info("unmarshalable", "bad", make(chan int))

	if pub.calls() != 0 {
		t.Errorf("publish calls = %d, want 0: malformed data must never reach the publisher", pub.calls())
	}

	failureNotices := 0
	for _, msg := range next.messages() {
		if msg != "unmarshalable" {
			failureNotices++
		}
	}
	if failureNotices != 1 {
		t.Errorf("failure notices on next = %d, want exactly 1", failureNotices)
	}
}

func TestSystemLogHandlerWithAttrsAndGroupsReachThePublishedEntry(t *testing.T) {
	pub := &fakePublisher{}
	next := &capturingHandler{}
	handler := NewSystemLogHandler(next, pub, 7)

	logger := slog.New(handler).With("task", "7").WithGroup("gate")
	logger.Warn("failed", "cmd", "go build ./...")

	if pub.calls() != 1 {
		t.Fatalf("publish calls = %d, want 1", pub.calls())
	}
	var entry logging.Entry
	if err := json.Unmarshal(pub.payloads[0], &entry); err != nil {
		t.Fatalf("payload is not a logging.Entry: %v", err)
	}
	if entry.Fields["task"] != "7" {
		t.Errorf("Fields[task] = %v, want 7 (WithAttrs before WithGroup keeps its own key ungrouped)", entry.Fields["task"])
	}
	if entry.Fields["gate.cmd"] != "go build ./..." {
		t.Errorf("Fields[gate.cmd] = %v, want %q", entry.Fields["gate.cmd"], "go build ./...")
	}
}

func TestSystemLogHandlerEnabledDelegatesToNext(t *testing.T) {
	next := &capturingHandler{}
	handler := NewSystemLogHandler(next, &fakePublisher{}, 1)
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled() = false, want true (next always returns true)")
	}
}
