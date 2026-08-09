package agentexec

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/logging"
)

// LogPublisher is the minimal capability SystemLogHandler needs: fire a
// message at a subject without waiting for a reply. Declared here rather
// than taking *nats.go's Conn or eventbus.Bus so the handler can be tested
// with a fake and never gains capabilities (subscribe, request/reply) it has
// no use for. *nats.Conn already satisfies this with no adapter needed.
type LogPublisher interface {
	Publish(subject string, data []byte) error
}

// SystemLogHandler tees slog records to a wrapped handler -- normally the
// agent's own stderr, kept for an operator attached to a live container --
// and additionally publishes each one, JSON-encoded as a logging.Entry, to
// SubjectForSystem(taskID): the return channel documented in subjects.go for
// "log dumps, health... observability" that previously had no publisher.
//
// The wire payload is a marshaled logging.Entry -- {"time","level","msg",
// "fields":{...}} -- meant to be round-tripped with json.Unmarshal straight
// back into a logging.Entry. This is deliberately NOT the same shape
// logging.decode() parses: decode() reads raw slog JSON *log lines* off
// disk, where every non-{time,level,msg} key sits at the top level and
// decode() moves it into Fields itself. Handing decode() this payload
// instead of json.Unmarshal would nest everything one level too deep, under
// a literal "fields" key, and silently lose every real field name. A future
// consumer of this subject (see GitHub issue #454) must unmarshal directly
// into logging.Entry, not call decode() on these bytes.
//
// Publishing is fire-and-forget. A core NATS Publish call is already
// non-blocking -- it queues to the connection's outbound buffer and returns,
// and the client itself drops writes rather than blocking once its
// reconnect buffer is full -- so this handler does not duplicate that
// behaviour with a second, home-grown bounded queue. A publish or marshal
// failure is reported once via the wrapped handler and then swallowed: log
// shipping must never block or fail the run it is reporting on, and a dead
// NATS connection is already visible through every stage subsequently
// failing to report its own result.
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

// warnAtMostOnce reports the first system-log delivery failure (marshal or
// publish, either way this record's own history is now missing on the
// daemon side) via the wrapped handler, then goes quiet -- a dead
// connection would otherwise turn every subsequent line into a second
// failure notice on stderr, drowning out the output an operator is trying
// to read.
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
