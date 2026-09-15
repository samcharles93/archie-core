package staterpc

import (
	"encoding/json"
	"fmt"
	"testing"
)

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
