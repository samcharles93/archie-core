package edastore

import "github.com/samcharles93/archie-core/internal/events"

// notifyWrite announces one successful write on the bindings or mappings
// tables. It rides the same activity-event path capture arrival uses: the
// State Store's event insert feeds the webui pump, which broadcasts to every
// connected browser, so an operator edit is visible without a manual
// refresh. The data carries the record id and action; the row itself stays
// the only source of the full record, and consumers refetch through the
// authenticated API -- the event is an invalidation signal, not a payload.
//
// A nil notifier is a silent no-op: stores composed without one (tests,
// tooling) keep working unchanged, mirroring the nil Publish on
// captureintake.Receiver.
func (s *Store) notifyWrite(kind, subject, action, id string) {
	if s.notify == nil {
		return
	}
	past := map[string]string{"create": "created", "update": "updated", "approve": "approved", "delete": "deleted"}[action]
	s.notify(events.Event{
		Kind:   kind,
		Detail: subject + " " + past,
		Data:   map[string]any{"id": id, "action": action},
	})
}
