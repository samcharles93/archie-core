package pluginextract

import (
	"reflect"
	"testing"
)

func TestSymbolsContainsExpectedPluginEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/plugin/plugin"
	pkg, ok := Symbols[pkgKey]
	if !ok {
		t.Fatalf("Symbols missing key %q", pkgKey)
	}

	// LoadDir must be a function.
	if v, ok := pkg["LoadDir"]; !ok {
		t.Error("LoadDir missing from Symbols")
	} else if v.Kind() != reflect.Func {
		t.Errorf("LoadDir kind = %v, want Func", v.Kind())
	}

	// Plugin must be a pointer type.
	if v, ok := pkg["Plugin"]; !ok {
		t.Error("Plugin missing from Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Plugin kind = %v, want Ptr", v.Kind())
	}
}

// The former TestGeneratedWrapperHasNilGuards was retired: it was named for a
// regression Yaegi cannot produce. Yaegi type-checks the assignment to an
// interface before building a wrapper, so a wrapper never reaches Go with a nil
// method field, and the hand-added guards it referred to only survived until the
// next regeneration. That invariant is pinned directly in internal/plugin by
// TestPartialImplementationIsRejectedBeforeWrapping and
// TestCompleteImplementationWrapsWithNoNilMethods.
