package nats

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// TaskEnvelope is the wire payload for a discovered issue.
//
// Publishing previously took seven positional parameters, six of them strings,
// which made transposed arguments a silent bug. Callers now build this struct.
type TaskEnvelope struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`

	// Labels are the issue's labels. On the wire this stays a
	// comma-separated string for compatibility with already-queued messages,
	// but in Go it is a slice -- the previous API passed a comma-joined
	// string and re-split it internally on every publish.
	Labels []string `json:"-"`

	// LabelsRaw is the comma-separated wire form. Kept in sync by MarshalJSON
	// and UnmarshalJSON; callers should use Labels.
	LabelsRaw string `json:"labels"`

	// Identity is the archie identity whose forge poll discovered the issue,
	// empty for single-identity deployments. Carried so the consuming daemon
	// enqueues the task under the right owner.
	Identity string `json:"identity,omitempty"`

	// Kind is the routing category, chosen by the publisher. Empty means
	// TaskKindDefault, which is also what messages queued before this field
	// existed decode to.
	Kind TaskKind `json:"kind,omitempty"`
}

// taskEnvelopeWire avoids infinite recursion in the JSON methods below.
type taskEnvelopeWire TaskEnvelope

// MarshalJSON flattens Labels into the comma-separated wire field.
func (t TaskEnvelope) MarshalJSON() ([]byte, error) {
	t.LabelsRaw = strings.Join(t.Labels, ",")
	data, err := json.Marshal(taskEnvelopeWire(t))
	if err != nil {
		return nil, fmt.Errorf("marshal task envelope: %w", err)
	}
	return data, nil
}

// UnmarshalJSON expands the comma-separated wire field into Labels.
func (t *TaskEnvelope) UnmarshalJSON(data []byte) error {
	var wire taskEnvelopeWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("unmarshal task envelope: %w", err)
	}
	*t = TaskEnvelope(wire)
	t.Labels = splitLabels(t.LabelsRaw)
	return nil
}

// DedupKey returns the JetStream Nats-Msg-Id for this task. Republishing the
// same issue inside Config.DedupWindow is suppressed by the server.
func (t TaskEnvelope) DedupKey() string {
	return fmt.Sprintf("%s%s/%s/%d", dedupKeyPrefix, t.Owner, t.Repo, t.Number)
}

// Subject returns the task subject this envelope routes to.
func (t TaskEnvelope) Subject() string { return SubjectForKind(t.Kind) }

// splitLabels parses the comma-separated wire form, dropping empty entries so
// "bug,,feature" and a trailing comma do not produce blank labels.
func splitLabels(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			labels = append(labels, trimmed)
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// Message is a broker message with the SDK type kept behind the boundary.
//
// Fetch previously handed callers a jetstream.Msg directly, so every consumer
// imported the NATS SDK and the broker stopped being replaceable.
type Message struct {
	msg jetstream.Msg
}

// Data returns the raw payload.
func (m Message) Data() []byte { return m.msg.Data() }

// Subject returns the subject the message arrived on.
func (m Message) Subject() string { return m.msg.Subject() }

// Header returns a single header value, or "" when absent.
func (m Message) Header(key string) string { return m.msg.Headers().Get(key) }

// ReplyAddress returns the requester's reply inbox, or ErrNoReplyAddress when
// the message carries none.
func (m Message) ReplyAddress() (string, error) {
	if addr := m.Header(ReplyHeader); addr != "" {
		return addr, nil
	}
	return "", fmt.Errorf("%w: header %s", ErrNoReplyAddress, ReplyHeader)
}

// Ack marks the message handled so JetStream will not redeliver it.
func (m Message) Ack() error {
	if err := m.msg.Ack(); err != nil {
		return fmt.Errorf("ack message on %s: %w", m.Subject(), err)
	}
	return nil
}

// Nak returns the message for redelivery.
func (m Message) Nak() error {
	if err := m.msg.Nak(); err != nil {
		return fmt.Errorf("nak message on %s: %w", m.Subject(), err)
	}
	return nil
}

// Task decodes the message as a TaskEnvelope.
func (m Message) Task() (TaskEnvelope, error) {
	var envelope TaskEnvelope
	if err := json.Unmarshal(m.Data(), &envelope); err != nil {
		return TaskEnvelope{}, fmt.Errorf("decode task on %s: %w", m.Subject(), err)
	}
	return envelope, nil
}
