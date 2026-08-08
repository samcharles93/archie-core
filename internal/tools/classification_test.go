package tools

import (
	"encoding/json"
	"testing"
)

func TestToolClassificationIsIdempotent(t *testing.T) {
	t.Run("zero value is not idempotent", func(t *testing.T) {
		var c ToolClassification
		if c.IsIdempotent() {
			t.Error("zero classification should not be idempotent")
		}
	})

	t.Run("explicit idempotent flag", func(t *testing.T) {
		if !ClassIdempotent.IsIdempotent() {
			t.Error("ClassIdempotent should be idempotent")
		}
	})

	t.Run("idempotent is not mutating", func(t *testing.T) {
		if ClassIdempotent.IsMutating() {
			t.Error("idempotent should not be mutating")
		}
	})
}

func TestToolClassificationIsMutating(t *testing.T) {
	t.Run("zero value is not mutating", func(t *testing.T) {
		var c ToolClassification
		if c.IsMutating() {
			t.Error("zero classification should not be mutating")
		}
	})

	t.Run("explicit mutating flag", func(t *testing.T) {
		if !ClassMutating.IsMutating() {
			t.Error("ClassMutating should be mutating")
		}
	})

	t.Run("mutating is not idempotent", func(t *testing.T) {
		if ClassMutating.IsIdempotent() {
			t.Error("mutating should not be idempotent")
		}
	})
}

func TestToolClassificationCombined(t *testing.T) {
	c := ClassIdempotent | ClassMutating
	if !c.IsIdempotent() {
		t.Error("combined should be idempotent")
	}
	if !c.IsMutating() {
		t.Error("combined should be mutating")
	}
}

func TestToolClassificationIsApprovalRequired(t *testing.T) {
	t.Run("zero value does not require approval", func(t *testing.T) {
		var c ToolClassification
		if c.IsApprovalRequired() {
			t.Error("zero classification should not require approval")
		}
	})

	t.Run("explicit approval flag", func(t *testing.T) {
		if !RequiresApproval.IsApprovalRequired() {
			t.Error("RequiresApproval should require approval")
		}
	})

	t.Run("approval is independent of idempotent", func(t *testing.T) {
		c := ClassIdempotent | RequiresApproval
		if !c.IsApprovalRequired() {
			t.Error("idempotent + approval should require approval")
		}
		if !c.IsIdempotent() {
			t.Error("idempotent + approval should still be idempotent")
		}
	})

	t.Run("approval is independent of mutating", func(t *testing.T) {
		c := ClassMutating | RequiresApproval
		if !c.IsApprovalRequired() {
			t.Error("mutating + approval should require approval")
		}
		if !c.IsMutating() {
			t.Error("mutating + approval should still be mutating")
		}
	})
}

func TestToolClassificationString(t *testing.T) {
	tests := []struct {
		c    ToolClassification
		want string
	}{
		{0, "default"},
		{ClassIdempotent, "idempotent"},
		{ClassMutating, "mutating"},
		{ClassIdempotent | ClassMutating, "idempotent|mutating"},
		{RequiresApproval, "requires_approval"},
		{ClassMutating | RequiresApproval, "mutating|requires_approval"},
		{ToolClassification(8), "unknown"},
	}

	for _, tt := range tests {
		got := tt.c.String()
		if got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestClassifyEntry(t *testing.T) {
	t.Run("idempotent tools are parallel-safe", func(t *testing.T) {
		e := ToolEntry{
			Name:           "reader",
			Handler:        noopHandler,
			Classification: ClassIdempotent,
		}
		if got := ClassifyEntry(e); got != ExecParallelSafe {
			t.Errorf("expected parallel-safe, got %v", got)
		}
	})

	t.Run("mutating tools are never-parallel", func(t *testing.T) {
		e := ToolEntry{
			Name:           "writer",
			Handler:        noopHandler,
			Classification: ClassMutating,
		}
		if got := ClassifyEntry(e); got != ExecNeverParallel {
			t.Errorf("expected never-parallel, got %v", got)
		}
	})

	t.Run("unclassified tools default to never-parallel", func(t *testing.T) {
		e := ToolEntry{Name: "unknown", Handler: noopHandler}
		if got := ClassifyEntry(e); got != ExecNeverParallel {
			t.Errorf("expected never-parallel, got %v", got)
		}
	})
}

func TestClassificationJSONRoundTrip(t *testing.T) {
	e := ToolEntry{
		Name:           "classified",
		Toolset:        "test",
		Description:    "A classified tool",
		Handler:        noopHandler,
		Classification: ClassIdempotent,
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Classification != ClassIdempotent {
		t.Errorf("Classification = %v after round-trip, want idempotent", decoded.Classification)
	}
}
