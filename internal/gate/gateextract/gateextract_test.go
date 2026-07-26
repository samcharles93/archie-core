package gateextract

import (
	"reflect"
	"testing"
)

func TestSymbolsContainsExpectedEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/gate/gate"
	pkg, ok := Symbols[pkgKey]
	if !ok {
		t.Fatalf("Symbols missing key %q", pkgKey)
	}

	// Blocking must be a function.
	if v, ok := pkg["Blocking"]; !ok {
		t.Error("Blocking not found in Symbols")
	} else if v.Kind() != reflect.Func {
		t.Errorf("Blocking kind = %v, want Func", v.Kind())
	}

	// Finding must be a pointer type.
	if v, ok := pkg["Finding"]; !ok {
		t.Error("Finding not found in Symbols")
	} else if v.Kind() != reflect.Ptr {
		t.Errorf("Finding kind = %v, want Ptr", v.Kind())
	}

	// GateContext must be a pointer type.
	if v, ok := pkg["GateContext"]; !ok {
		t.Error("GateContext not found in Symbols")
	} else if v.Kind() != reflect.Ptr {
		t.Errorf("GateContext kind = %v, want Ptr", v.Kind())
	}
}

func TestSymbolsNoUnexpectedEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/gate/gate"
	pkg := Symbols[pkgKey]
	expected := map[string]bool{
		"Blocking":    true,
		"Finding":     true,
		"GateContext": true,
	}
	for name := range pkg {
		if !expected[name] {
			t.Errorf("unexpected symbol %q in gateextract Symbols", name)
		}
	}
}
