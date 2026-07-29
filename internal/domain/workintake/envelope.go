// Package workintake owns the promotion of a discovered external issue into
// durable, workflow-backed work.
//
// It owns the vocabulary of that promotion -- the task envelope, its routing
// kind, and the subjects it is addressed to -- because the domain that
// defines a message's meaning owns its schema. These previously lived in the
// NATS package, which made the transport responsible for knowing what a unit
// of work was; see docs/architecture/dependencies-and-contracts.md.
//
// This package names no broker. It produces and consumes bytes; carrying
// them is internal/eventbus's job.
package workintake

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// dedupKeyPrefix namespaces idempotency keys so they cannot collide with
// another producer's on a shared broker.
const dedupKeyPrefix = "archie:"

// TaskEnvelope is the wire payload announcing a discovered issue.
//
// Publishing once took seven positional parameters, six of them strings,
// which made a transposed argument a silent bug. Callers build this struct.
type TaskEnvelope struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`

	// Labels are the issue's labels. On the wire this stays a
	// comma-separated string for compatibility with already-queued messages,
	// but in Go it is a slice.
	Labels []string `json:"-"`

	// LabelsRaw is the comma-separated wire form, kept in sync by the JSON
	// methods. Callers should use Labels.
	LabelsRaw string `json:"labels"`

	// Identity is the archie identity whose forge poll discovered the issue,
	// empty for single-identity deployments. Carried so the consuming daemon
	// enqueues the task under the right owner.
	Identity string `json:"identity,omitempty"`

	// Kind is the routing category, chosen by the publisher. Empty means
	// KindDefault, which is also what messages queued before this field
	// existed decode to.
	Kind Kind `json:"kind,omitempty"`
}

// envelopeWire avoids infinite recursion in the JSON methods below.
type envelopeWire TaskEnvelope

// MarshalJSON flattens Labels into the comma-separated wire field.
func (t TaskEnvelope) MarshalJSON() ([]byte, error) {
	t.LabelsRaw = strings.Join(t.Labels, ",")
	data, err := json.Marshal(envelopeWire(t))
	if err != nil {
		return nil, fmt.Errorf("marshal task envelope: %w", err)
	}
	return data, nil
}

// UnmarshalJSON expands the comma-separated wire field into Labels.
func (t *TaskEnvelope) UnmarshalJSON(data []byte) error {
	var wire envelopeWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("unmarshal task envelope: %w", err)
	}
	*t = TaskEnvelope(wire)
	t.Labels = SplitLabels(t.LabelsRaw)
	return nil
}

// Encode renders the envelope for transport.
func (t TaskEnvelope) Encode() ([]byte, error) {
	data, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("encode task %s: %w", t.Ref(), err)
	}
	return data, nil
}

// DecodeTask parses a transported envelope.
func DecodeTask(data []byte) (TaskEnvelope, error) {
	var envelope TaskEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return TaskEnvelope{}, fmt.Errorf("decode task envelope: %w", err)
	}
	return envelope, nil
}

// Ref identifies the issue for logs and error messages.
func (t TaskEnvelope) Ref() string {
	return t.Owner + "/" + t.Repo + "#" + strconv.Itoa(t.Number)
}

// IdempotencyKey identifies this issue for delivery deduplication, so
// rediscovering it on a later poll does not enqueue the same work twice.
func (t TaskEnvelope) IdempotencyKey() string {
	return dedupKeyPrefix + t.Owner + "/" + t.Repo + "/" + strconv.Itoa(t.Number)
}

// Subject returns the address this envelope routes to.
func (t TaskEnvelope) Subject() string { return SubjectForKind(t.Kind) }

// SplitLabels parses the comma-separated wire form, dropping empties so
// "bug,,feature" and a trailing comma do not produce blank labels.
func SplitLabels(raw string) []string {
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
