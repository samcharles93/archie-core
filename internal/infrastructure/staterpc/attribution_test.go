package staterpc

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// TestEventAttributionSurvivesTheStateStoreBoundary runs the same insert through
// the local adapter and through a bufconn-backed gRPC client, because that hop is
// where the attribution was being lost: the daemon records the event and the State
// Store holds it, so a field the contract does not carry is a field the record
// never has.
//
// The failure this pins is specific and was live: the actor was named inside the
// event's detail text while actor_id, actor_kind and principal_id were all empty,
// so an audit query reading the columns found nothing and "who approved this"
// meant parsing a human-readable string. An in-process test passed throughout,
// because it never crossed this boundary.
func TestEventAttributionSurvivesTheStateStoreBoundary(t *testing.T) {
	const (
		actorID     = "70000000-0000-5000-8000-000000000001"
		principalID = "70000000-0000-5000-8000-000000000002"
		actorKind   = "bot"
	)

	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			local := pgstore.Open(t)
			var c contract = local
			if mode == "grpc" {
				c = remoteTaskStore(t, local, nil, nil)
			}

			task, err := c.EnqueueChatTask(ctx, "acme", "widget", "title", "body", "implement", "")
			if err != nil || task == nil {
				t.Fatalf("EnqueueChatTask = (%+v, %v)", task, err)
			}

			recorded := events.Event{
				Kind:        events.KindAgentApproved,
				TaskID:      task.ID,
				ActorID:     actorID,
				ActorKind:   actorKind,
				PrincipalID: principalID,
				Detail:      "approved by an agent, authorised by a person",
			}
			if _, err := c.InsertEvent(ctx, recorded); err != nil {
				t.Fatalf("InsertEvent: %v", err)
			}

			got, err := c.TaskEvents(ctx, task.ID)
			if err != nil {
				t.Fatalf("TaskEvents: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("TaskEvents returned %d events, want 1", len(got))
			}
			event := got[0]
			if event.ActorID != actorID || event.ActorKind != actorKind || event.PrincipalID != principalID {
				t.Fatalf("attribution did not cross the boundary: actor=%q kind=%q principal=%q",
					event.ActorID, event.ActorKind, event.PrincipalID)
			}
			if event.Kind != events.KindAgentApproved {
				t.Fatalf("kind = %q, want %q", event.Kind, events.KindAgentApproved)
			}
		})
	}
}
