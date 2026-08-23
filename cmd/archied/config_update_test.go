package main

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestApplyDottedOverlayNestsBeforeDecode pins the PATCH-path fix: the
// UI sends dotted keys ("budgets.max_steps") and yaml cannot decode a
// dotted key as a struct field, so the updates must be nested first. A
// regression here silently drops every dotted update while returning 200.
func TestApplyDottedOverlayNestsBeforeDecode(t *testing.T) {
	cfg := config.Config{Budgets: config.Budgets{MaxSteps: 10}}
	if err := applyDottedOverlay(&cfg, map[string]any{"budgets.max_steps": 20}); err != nil {
		t.Fatal(err)
	}
	if cfg.Budgets.MaxSteps != 20 {
		t.Fatalf("MaxSteps = %d, want 20 (dotted key must be nested before decode)", cfg.Budgets.MaxSteps)
	}
}

// TestApplyDottedOverlayRejectsEmptyKey pins that a malformed dotted key
// fails instead of silently nesting under a synthetic root.
func TestApplyDottedOverlayRejectsEmptyKey(t *testing.T) {
	cfg := config.Config{}
	if err := applyDottedOverlay(&cfg, map[string]any{"": 1}); err == nil {
		t.Fatal("empty dotted key accepted")
	}
}
