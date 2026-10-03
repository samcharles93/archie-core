package agentexec

import (
	"encoding/json"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/events"
)

// EventPublisher publishes a message to a subject without waiting.
type EventPublisher interface {
	Publish(subject string, data []byte) error
}

// ForwardTaskEvents publishes every event from sub to SubjectForEvents(taskID)
// until sub closes. Failures are logged.
func ForwardTaskEvents(sub *events.Sub, pub EventPublisher, taskID int64, log *slog.Logger) {
	subject := SubjectForEvents(taskID)
	for e := range sub.C {
		data, err := json.Marshal(e)
		if err != nil {
			log.Warn("task event marshal failed", "task", taskID, "kind", e.Kind, "err", err)
			continue
		}
		if err := pub.Publish(subject, data); err != nil {
			log.Warn("task event publish failed", "task", taskID, "kind", e.Kind, "err", err)
		}
	}
}
