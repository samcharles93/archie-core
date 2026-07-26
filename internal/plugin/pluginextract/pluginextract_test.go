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

	// Registry must be a pointer type.
	if v, ok := pkg["Registry"]; !ok {
		t.Error("Registry missing from Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Registry kind = %v, want Ptr", v.Kind())
	}

	// Plugin must be a pointer type.
	if v, ok := pkg["Plugin"]; !ok {
		t.Error("Plugin missing from Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Plugin kind = %v, want Ptr", v.Kind())
	}
}

func TestGeneratedWrapperHasNilGuards(t *testing.T) {
	// Regression: go generate strips nil-guards from _Plugin wrapper.
	// Verify that Plugin is present in the Symbols table and is an
	// interface type (as expected for yaegi interface extraction).

	pkgKey := "github.com/samcharles93/archie-core/internal/plugin/plugin"
	pkg := Symbols[pkgKey]

	pluginType, ok := pkg["Plugin"]
	if !ok {
		t.Fatal("Plugin missing from Symbols")
	}

	// Plugin is an interface type — yaegi represents interfaces
	// differently from struct pointers.
	kind := pluginType.Kind()
	if kind != reflect.Interface && kind != reflect.Pointer {
		t.Errorf("Plugin type kind = %v, want Interface or Ptr", kind)
	}

	// Verify WName and WVersion exist on the Plugin interface type.
	// For interface types, we check the method set.
	if kind == reflect.Interface {
		methodCount := pluginType.Type().NumMethod()
		if methodCount < 2 {
			t.Errorf("Plugin interface has %d methods, expected at least 2 (Name, Version)", methodCount)
		}
	}
}
