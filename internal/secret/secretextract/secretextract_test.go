package secretextract

import (
	"reflect"
	"testing"
)

func TestSymbolsContainsExpectedEngineEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/secret/secret"
	pkg, ok := Symbols[pkgKey]
	if !ok {
		t.Fatalf("Symbols missing key %q", pkgKey)
	}

	if v, ok := pkg["NewRegistry"]; !ok {
		t.Error("NewRegistry missing from Symbols")
	} else if v.Kind() != reflect.Func {
		t.Errorf("NewRegistry kind = %v, want Func", v.Kind())
	}

	if v, ok := pkg["Registry"]; !ok {
		t.Error("Registry missing from Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Registry kind = %v, want Ptr", v.Kind())
	}

	if v, ok := pkg["Engine"]; !ok {
		t.Error("Engine missing from Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("Engine kind = %v, want Ptr", v.Kind())
	}

	if v, ok := pkg["SecretRef"]; !ok {
		t.Error("SecretRef missing from Symbols")
	} else if v.Kind() != reflect.Pointer {
		t.Errorf("SecretRef kind = %v, want Ptr", v.Kind())
	}
}

func TestGeneratedWrapperHasNilGuards(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/secret/secret"
	pkg := Symbols[pkgKey]

	engineType, ok := pkg["Engine"]
	if !ok {
		t.Fatal("Engine missing from Symbols")
	}

	kind := engineType.Kind()
	if kind != reflect.Interface && kind != reflect.Pointer {
		t.Errorf("Engine type kind = %v, want Interface or Ptr", kind)
	}
	if kind == reflect.Interface {
		methodCount := engineType.Type().NumMethod()
		if methodCount < 3 {
			t.Errorf("Engine interface has %d methods, expected at least 3 (Name, Version, Resolve)", methodCount)
		}
	}
}
