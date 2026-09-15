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

	// LevelError and LevelWarn must be the exported level constants.
	for _, name := range []string{"LevelError", "LevelWarn"} {
		if v, ok := pkg[name]; !ok {
			t.Errorf("%s not found in Symbols", name)
		} else if v.Kind() != reflect.String {
			t.Errorf("%s kind = %v, want String", name, v.Kind())
		}
	}

	// Finding must be a pointer type.
	if v, ok := pkg["Finding"]; !ok {
		t.Error("Finding not found in Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Finding kind = %v, want Ptr", v.Kind())
	}

	// Level must be a pointer to the defined string type, so interpreted
	// scripts can resolve Finding.Level's named type.
	if v, ok := pkg["Level"]; !ok {
		t.Error("Level not found in Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Level kind = %v, want Ptr", v.Kind())
	}

	// GateContext must be a pointer type.
	if v, ok := pkg["GateContext"]; !ok {
		t.Error("GateContext not found in Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("GateContext kind = %v, want Ptr", v.Kind())
	}
}

func TestSymbolsNoUnexpectedEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/gate/gate"
	pkg := Symbols[pkgKey]
	expected := map[string]bool{
		"Blocking":    true,
		"LevelError":  true,
		"LevelWarn":   true,
		"Finding":     true,
		"GateContext": true,
		"Level":       true,
	}
	for name := range pkg {
		if !expected[name] {
			t.Errorf("unexpected symbol %q in gateextract Symbols", name)
		}
	}
}
