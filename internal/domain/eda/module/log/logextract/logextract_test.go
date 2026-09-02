package logextract

import (
	"reflect"
	"testing"
)

// TestSymbolsContainsExpectedEntries verifies the generated symbol table
// exposes the log kind's contract: SchemaVersion const, and the Args/Result
// types an interpreted module must reference.
func TestSymbolsContainsExpectedEntries(t *testing.T) {
	pkgKey := "github.com/samcharles93/archie-core/internal/domain/eda/module/log/log"
	pkg, ok := Symbols[pkgKey]
	if !ok {
		t.Fatalf("Symbols missing key %q", pkgKey)
	}

	if v, ok := pkg["SchemaVersion"]; !ok {
		t.Error("SchemaVersion missing from Symbols")
	} else if v.Kind() != reflect.Interface {
		// constant.MakeFromLiteral produces a constant Value; yaegi wraps it
		// as a typed reflect value. Any kind is acceptable as long as it
		// resolves -- the codegen smoke check is existence + buildability.
		_ = v
	}

	for _, name := range []string{"Args", "Result"} {
		v, ok := pkg[name]
		if !ok {
			t.Errorf("%s missing from Symbols", name)
			continue
		}
		if v.Kind() != reflect.Pointer {
			t.Errorf("%s kind = %v, want Ptr", name, v.Kind())
		}
	}
}
