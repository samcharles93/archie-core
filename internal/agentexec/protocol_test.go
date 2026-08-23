package agentexec

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestProtocolRoundTrip(t *testing.T) {
	want := Request{
		Version: ProtocolVersion, TaskID: 42, Attempt: 3, Stage: "fix", Model: "openai/gpt-5",
		Mission: "fix it", Budget: Budget{MaxSteps: 12, WallClock: 2 * time.Minute},
		Gate:         Gate{Commands: []Command{{Name: "go", Argv: []string{"go", "test", "./..."}}}, MaxConsecutiveFailures: 2},
		Protection:   Protection{Suffixes: []string{"_templ.go"}, Globs: []string{"*_test.go"}},
		CaptureTools: []CaptureTool{{Name: "decide", Parameters: json.RawMessage(`{"type":"object"}`), MaxCalls: 1}},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestRequestRejectsUnknownProtocol(t *testing.T) {
	req := Request{Version: ProtocolVersion + 1, TaskID: 1, Stage: "plan", Model: "provider/model"}
	if err := req.Validate(); err == nil {
		t.Fatal("Validate accepted an unsupported protocol version")
	}
}

func TestResultRejectsMismatchedIdentity(t *testing.T) {
	req := Request{Version: ProtocolVersion, TaskID: 1, Attempt: 2, Stage: "build", Model: "provider/model"}
	result := Result{
		Version: ProtocolVersion, TaskID: 1, Attempt: 3, Stage: "build", Status: StatusPassed,
	}
	if err := result.ValidateFor(req); err == nil {
		t.Fatal("ValidateFor accepted a result from another attempt")
	}
}
