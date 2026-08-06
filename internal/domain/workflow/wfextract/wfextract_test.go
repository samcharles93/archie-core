package wfextract

import (
	"reflect"
	"testing"
)

func TestSymbolsContainsExpectedWorkflowEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/domain/workflow/workflow"
	pkg, ok := Symbols[pkgKey]
	if !ok {
		t.Fatalf("Symbols missing key %q", pkgKey)
	}

	// Core functions that must be present.
	requiredFuncs := []string{
		"Route", "Run", "Bootstrap", "Feasibility", "Implement",
		"OpenPR", "TDD",
	}
	for _, name := range requiredFuncs {
		v, ok := pkg[name]
		if !ok {
			t.Errorf("required function %q missing from Symbols", name)
			continue
		}
		if v.Kind() != reflect.Func {
			t.Errorf("%s kind = %v, want Func", name, v.Kind())
		}
	}

	// Core types that must be present as pointers.
	requiredTypes := []string{
		"AgentStage", "Outcome", "Registry", "Stage",
		"TaskContext", "Workflow",
	}
	for _, name := range requiredTypes {
		v, ok := pkg[name]
		if !ok {
			t.Errorf("required type %q missing from Symbols", name)
			continue
		}
		if v.Kind() != reflect.Pointer {
			t.Errorf("%s kind = %v, want Ptr", name, v.Kind())
		}
	}
}

func TestWFExtractSymbolCountReasonable(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/domain/workflow/workflow"
	pkg := Symbols[pkgKey]
	// Should have at least the core functions + types (10+ entries).
	if len(pkg) < 10 {
		t.Errorf("expected at least 10 symbols, got %d", len(pkg))
	}
}
