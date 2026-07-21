---
description: Source module internal/agentexec/protocol_test.go (46 lines).
resource: internal/agentexec/protocol_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: protocol_test.go
type: Module
---

# Module protocol_test.go

**Path**: `internal/agentexec/protocol_test.go`  
**Lines**: 46

## Snippet Preview

```
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
		Mission: "fix it", Budget: Budget{MaxSteps: 12, MaxTokens: 4000, WallClock: 2 * time.Minute},
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

```
