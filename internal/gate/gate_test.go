package gate

import (
	"testing"
)

func TestBlocking(t *testing.T) {
	t.Run("nil slice returns false", func(t *testing.T) {
		if Blocking(nil) {
			t.Error("Blocking(nil) should return false")
		}
	})

	t.Run("empty slice returns false", func(t *testing.T) {
		if Blocking([]Finding{}) {
			t.Error("Blocking([]) should return false")
		}
	})

	t.Run("warn-only returns false", func(t *testing.T) {
		findings := []Finding{
			{Level: "warn", Message: "advisory only"},
			{Level: "warn", Message: "another advisory"},
		}
		if Blocking(findings) {
			t.Error("warn-only findings should not be blocking")
		}
	})

	t.Run("single error returns true", func(t *testing.T) {
		findings := []Finding{
			{Level: "error", Message: "must fix"},
		}
		if !Blocking(findings) {
			t.Error("error-level finding should be blocking")
		}
	})

	t.Run("mixed error and warn returns true", func(t *testing.T) {
		findings := []Finding{
			{Level: "warn", Message: "advisory"},
			{Level: "error", Message: "blocker"},
		}
		if !Blocking(findings) {
			t.Error("mixed findings with at least one error should be blocking")
		}
	})

	t.Run("error as first element returns true", func(t *testing.T) {
		findings := []Finding{
			{Level: "error", Message: "first"},
			{Level: "warn", Message: "second"},
		}
		if !Blocking(findings) {
			t.Error("error as first element should be blocking")
		}
	})

	t.Run("case-sensitive level check", func(t *testing.T) {
		// Only lowercase "error" blocks; "ERROR" does not.
		findings := []Finding{
			{Level: "ERROR", Message: "uppercase error"},
			{Level: "Error", Message: "title-case error"},
		}
		if Blocking(findings) {
			t.Error("non-lowercase error should not be blocking")
		}
	})

	t.Run("unknown level does not block", func(t *testing.T) {
		findings := []Finding{
			{Level: "unknown", Message: "unanticipated level"},
		}
		if Blocking(findings) {
			t.Error("unknown level should not be blocking")
		}
	})

	t.Run("empty level does not block", func(t *testing.T) {
		findings := []Finding{
			{Level: "", Message: "missing level"},
		}
		if Blocking(findings) {
			t.Error("empty level should not be blocking")
		}
	})

	t.Run("large finding slice returns early on first error", func(t *testing.T) {
		// Blocking returns true on the first error — it does not scan
		// the entire slice. Verify this with 1 error buried among warns.
		findings := make([]Finding, 1000)
		for i := range findings {
			findings[i] = Finding{Level: "warn", Message: "advisory"}
		}
		findings[500] = Finding{Level: "error", Message: "blocker at 500"}
		if !Blocking(findings) {
			t.Error("error at position 500 should still be detected")
		}
	})
}

func TestGateContextZeroValue(t *testing.T) {
	// Zero-value GateContext should not panic when accessed.
	var gctx GateContext
	if gctx.Dir != "" {
		t.Errorf("zero Dir should be empty, got %q", gctx.Dir)
	}
	if gctx.BaseRef != "" {
		t.Errorf("zero BaseRef should be empty, got %q", gctx.BaseRef)
	}
	if gctx.Repo != "" {
		t.Errorf("zero Repo should be empty, got %q", gctx.Repo)
	}
	if gctx.Diff != "" {
		t.Errorf("zero Diff should be empty")
	}
	if gctx.ChangedFiles != nil {
		t.Errorf("zero ChangedFiles should be nil, got %v", gctx.ChangedFiles)
	}
}

func TestGateContextFieldIntegrity(t *testing.T) {
	gctx := GateContext{
		Diff:         "--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old\n+new",
		ChangedFiles: []string{"file.go", "other.go"},
		Dir:          "/tmp/worktree",
		BaseRef:      "origin/main",
		Repo:         "owner/name",
	}

	if gctx.Dir != "/tmp/worktree" {
		t.Errorf("Dir = %q", gctx.Dir)
	}
	if gctx.BaseRef != "origin/main" {
		t.Errorf("BaseRef = %q", gctx.BaseRef)
	}
	if gctx.Repo != "owner/name" {
		t.Errorf("Repo = %q", gctx.Repo)
	}
	if len(gctx.ChangedFiles) != 2 {
		t.Errorf("ChangedFiles = %v, expected 2 entries", gctx.ChangedFiles)
	}
}

func TestFindingConstruction(t *testing.T) {
	t.Run("full finding", func(t *testing.T) {
		f := Finding{
			Level:   "error",
			File:    "main.go",
			Line:    42,
			Message: "nil pointer dereference possible",
		}
		if f.Level != "error" {
			t.Errorf("Level = %q", f.Level)
		}
		if f.File != "main.go" {
			t.Errorf("File = %q", f.File)
		}
		if f.Line != 42 {
			t.Errorf("Line = %d", f.Line)
		}
		if f.Message != "nil pointer dereference possible" {
			t.Errorf("Message = %q", f.Message)
		}
	})

	t.Run("minimal finding (level + message only)", func(t *testing.T) {
		f := Finding{Level: "warn", Message: "review needed"}
		if f.File != "" {
			t.Errorf("File should be empty, got %q", f.File)
		}
		if f.Line != 0 {
			t.Errorf("Line should be 0, got %d", f.Line)
		}
	})

	t.Run("zero-value finding", func(t *testing.T) {
		var f Finding
		if f.Level != "" {
			t.Errorf("zero Level should be empty, got %q", f.Level)
		}
		if f.Message != "" {
			t.Errorf("zero Message should be empty, got %q", f.Message)
		}
	})

	t.Run("negative line accepted as data", func(t *testing.T) {
		// Line is an int with no validation — negative values are
		// stored as-is. Tests document this behavior.
		f := Finding{Level: "warn", Line: -1, Message: "test"}
		if f.Line != -1 {
			t.Errorf("Line should be -1, got %d", f.Line)
		}
	})
}

func TestFindingSliceBlocking(t *testing.T) {
	// End-to-end: construct findings and check if they block.
	t.Run("all clear", func(t *testing.T) {
		findings := []Finding{
			{Level: "warn", File: "a.go", Line: 1, Message: "consider refactoring"},
			{Level: "warn", File: "b.go", Line: 10, Message: "possible simplification"},
		}
		if Blocking(findings) {
			t.Error("all-warn should not block")
		}
	})

	t.Run("one blocker among many", func(t *testing.T) {
		findings := []Finding{
			{Level: "warn", File: "x.go", Message: "first"},
			{Level: "warn", File: "y.go", Message: "second"},
			{Level: "error", File: "z.go", Line: 99, Message: "critical issue"},
			{Level: "warn", File: "w.go", Message: "fourth"},
		}
		if !Blocking(findings) {
			t.Error("single error among warns should block")
		}
	})
}
