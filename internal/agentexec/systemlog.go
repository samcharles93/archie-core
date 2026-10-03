package agentexec

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/logging"
)

// LogPublisher publishes a message to a subject without waiting.
type LogPublisher interface {
	Publish(subject string, data []byte) error
}

// SystemLogHandler writes slog records to a wrapped handler and publishes
// each as a JSON logging.Entry to SubjectForSystem(taskID). Publish failures
// are reported once and never block.
type SystemLogHandler struct {
	next    slog.Handler
	pub     LogPublisher
	subject string

	attrs  []slog.Attr
	groups []string

	// warnOnce is a pointer, shared across every clone WithAttrs/WithGroup
	// produce from one root handler: sync.Once is not safe to copy by value
	// (go vet: copylocks), and the "log the publish failure once" intent is
	// per underlying connection, not per derived logger.
	warnOnce *sync.Once
}

// NewSystemLogHandler wraps next for taskID's system log subject.
func NewSystemLogHandler(next slog.Handler, pub LogPublisher, taskID int64) *SystemLogHandler {
	return &SystemLogHandler{next: next, pub: pub, subject: SubjectForSystem(taskID), warnOnce: &sync.Once{}}
}

func (h *SystemLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *SystemLogHandler) Handle(ctx context.Context, record slog.Record) error {
	entry := logging.Entry{
		Time:    record.Time,
		Level:   record.Level.String(),
		Message: record.Message,
		Fields:  logging.FlattenAttrs(record, h.attrs, h.groups),
	}
	data, err := json.Marshal(entry)
	if err == nil {
		err = h.pub.Publish(h.subject, data)
	}
	if err != nil {
		h.warnAtMostOnce(ctx, record.Time, err)
	}
	return h.next.Handle(ctx, record)
}

// warnAtMostOnce reports the first publish failure only.
func (h *SystemLogHandler) warnAtMostOnce(ctx context.Context, at time.Time, cause error) {
	h.warnOnce.Do(func() {
		warning := slog.NewRecord(at, slog.LevelWarn,
			"system log delivery failed; further failures on this connection are not logged individually", 0)
		warning.AddAttrs(slog.String("err", cause.Error()))
		_ = h.next.Handle(ctx, warning)
	})
}

// WithAttrs bakes the current group prefix into each new attr's key
// immediately -- see logging.PrefixAttrs' sibling logic in feed.go for why
// deferring it to Handle-time is wrong: it would nest an attr under a group
// added by a *later* WithGroup call it was never actually inside.
func (h *SystemLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.next = h.next.WithAttrs(attrs)
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), logging.PrefixAttrs(attrs, h.groups)...)
	return &clone
}

func (h *SystemLogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.next = h.next.WithGroup(name)
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}
