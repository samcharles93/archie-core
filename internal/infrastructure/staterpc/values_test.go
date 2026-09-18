package staterpc

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
)

func TestEventAttemptSurvivesTheWire(t *testing.T) {
	// attempt is provenance, so it must cross the boundary or the dashboard
	// cannot attribute an event to a run. Zero must survive too: it is the
	// unattributed marker, and a mapping that turned it into an absent field
	// would make "unattributed" unrepresentable.
	for _, attempt := range []int{0, 1, 7} {
		t.Run(fmt.Sprintf("attempt=%d", attempt), func(t *testing.T) {
			original := events.Event{
				Kind: events.KindStageFinish, TaskID: 42, Stage: "implement",
				Attempt: attempt, Detail: "done",
				Data: map[string]any{"duration_ms": float64(1200)},
			}
			got := eventValue(eventProto(original))
			if got.Attempt != attempt {
				t.Fatalf("attempt round-trip = %d, want %d", got.Attempt, attempt)
			}
			if got.Kind != original.Kind || got.TaskID != original.TaskID || got.Stage != original.Stage {
				t.Fatalf("event round-trip = %+v, want %+v", got, original)
			}
			if got.Data["duration_ms"] != float64(1200) {
				t.Fatalf("data round-trip = %v", got.Data)
			}
		})
	}
}

func TestConfigCapturedPayloadSurvivesTheWire(t *testing.T) {
	// R4's document is a nested JSON object inside the event's data, so the
	// round trip has to keep its structure rather than flattening it to a
	// string: the Config tab reads fields out of it. The key is the daemon's own
	// -- internal/daemon/daemon.go writes the document under "document" -- and
	// is asserted by name here so a rename cannot ride along unnoticed.
	document := map[string]any{
		"schema": events.ConfigCapturedSchema,
		"document": map[string]any{
			"bot_user":       "archie",
			"diff_cap_lines": float64(4000),
			"models":         map[string]any{"implement": "anthropic/claude"},
		},
	}
	got := eventValue(eventProto(events.Event{
		Kind: events.KindConfigCaptured, TaskID: 42, Attempt: 2, Data: document,
	}))
	if got.Kind != events.KindConfigCaptured || got.Attempt != 2 {
		t.Fatalf("config event = %+v", got)
	}
	if _, renamed := got.Data["config"]; renamed {
		t.Error(`the document crossed under "config"; the producer writes it under "document"`)
	}
	if len(got.Data) != 2 {
		t.Errorf("data keys = %v, want exactly schema and document", got.Data)
	}
	doc, ok := got.Data["document"].(map[string]any)
	if !ok {
		t.Fatalf("document payload = %#v, want a decoded object under the producer's own key", got.Data)
	}
	if doc["bot_user"] != "archie" {
		t.Errorf("document.bot_user = %v, want archie", doc["bot_user"])
	}
	models, ok := doc["models"].(map[string]any)
	if !ok || models["implement"] != "anthropic/claude" {
		t.Errorf("document.models = %#v, want the nested map preserved", doc["models"])
	}
	if got.Data["schema"] != events.ConfigCapturedSchema {
		t.Errorf("schema = %v, want %q", got.Data["schema"], events.ConfigCapturedSchema)
	}
}

func TestTaskLogStageFilterSurvivesTheWire(t *testing.T) {
	// The stage filter is only useful if it reaches the reader, and the wire
	// mirrors the whole logging.Query for exactly that reason: a filter that
	// silently stops working at the boundary looks like "no matches".
	query := logging.Query{
		Levels: []string{"ERROR"}, Component: "daemon", Stage: "implement", Contains: "parked", Limit: 50,
	}
	got := taskLogQueryValue(taskLogRequestProto(42, 3, query))
	if got.Stage != "implement" {
		t.Fatalf("stage round-trip = %q, want implement", got.Stage)
	}
	if got.Component != "daemon" || got.Contains != "parked" || got.Limit != 50 {
		t.Fatalf("query round-trip = %+v, want the whole filter preserved", got)
	}
}

// TestEventDataJSONPreservesMarshalErrorMarker pins §11's conformance
// invariant: the local store (internal/store/events.go) records a
// {"marshal_error":%q} marker when json.Marshal of event Data fails, so the
// remote wire must carry the same marker rather than silently emptying the
// Data on the hop.
func TestEventDataJSONPreservesMarshalErrorMarker(t *testing.T) {
	data := map[string]any{"channel": make(chan int)}

	got := eventDataJSON(data)
	if got == "" {
		t.Fatal(`eventDataJSON = "", want the local adapter's marshal_error marker`)
	}

	_, err := json.Marshal(data)
	if err == nil {
		t.Fatal("test input must fail json.Marshal")
	}
	want := fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
	if got != want {
		t.Fatalf("eventDataJSON = %q, want %q (local-adapter fidelity)", got, want)
	}

	// The server side rehydrates the marker back into a Data map, exactly
	// as the local store's scanEvents rehydrates its on-disk column.
	round := eventDataValue(got)
	message, ok := round["marshal_error"].(string)
	if !ok || message == "" {
		t.Fatalf("eventDataValue = %#v, want a non-empty marshal_error string", round)
	}
}
