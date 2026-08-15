package agentexec

import (
	"encoding/json"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/events"
)

// EventPublisher is the minimal capability needed to ship a task's
// observability events to the daemon over NATS: fire a message at a subject
// without waiting for a reply. Declared here rather than taking *nats.go's
// Conn so ForwardTaskEvents can be tested with a fake. *nats.Conn already
// satisfies this with no adapter needed.
type EventPublisher interface {
	Publish(subject string, data []byte) error
}

// ForwardTaskEvents relays every event published on sub to pub, JSON-encoded,
// on SubjectForEvents(taskID), until sub is closed.
//
// A workflow.TaskContext built inside an archie-agent worker publishes to an
// in-process *events.Bus the daemon cannot see -- the worker and the daemon
// are separate processes connected only by NATS. This is the bridge that
// makes those Emit calls (stage progress, outcome, parking) reach the
// daemon's events table and dashboard timeline the same way an in-process,
// daemon-run workflow's Bus already does.
//
// A marshal or publish failure is logged and the loop continues: one missing
// event must never abort the ones after it, and event shipping must never
// block or fail the workflow run it is reporting on.
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
