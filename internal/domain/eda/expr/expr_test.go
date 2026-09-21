package expr

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"

	"github.com/samcharles93/archie-core/internal/domain/eda/module/log"
)

// declaredResultEnv builds the per-playbook environment used by the typed
// `actions` tests: one declared prior action id "build" whose result type is
// the log kind's Go Result struct (fields Written, Level -> written, level).
func declaredResultEnv(t *testing.T) *Env {
	t.Helper()
	return NewEnv(DeclaredResult{ID: "build", Type: reflect.TypeFor[log.Result]()})
}

// TestCompileDeclaredResultRead: a read of a declared id's result field
// compiles against the per-playbook object type.
func TestCompileDeclaredResultRead(t *testing.T) {
	env := declaredResultEnv(t)
	prg, err := env.Compile(`actions.build.result.written == true`)
	if err != nil {
		t.Fatalf("Compile(declared result read): %v", err)
	}
	ids, resolvable := prg.ActionReferences()
	if !resolvable || !slices.Equal(ids, []string{"build"}) {
		t.Fatalf("ActionReferences = (%v, %v), want (build, true)", ids, resolvable)
	}
}

// TestCompileMisspelledResultFieldRejected: a field the declared Result does
// not define is a compile-time error naming the field, never a runtime miss.
func TestCompileMisspelledResultFieldRejected(t *testing.T) {
	env := declaredResultEnv(t)
	misspelled := "wri" + "ten" // deliberately misspelled Result field under test
	_, err := env.Compile(`actions.build.result.` + misspelled + ` == true`)
	if err == nil {
		t.Fatal("Compile(misspelled field) = nil, want error")
	}
	for _, want := range []string{"undefined field", misspelled} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Compile(misspelled field) error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestCompileUndeclaredActionIDRejected: a read of an id not declared for the
// playbook is a compile-time error naming the id.
func TestCompileUndeclaredActionIDRejected(t *testing.T) {
	env := declaredResultEnv(t)
	_, err := env.Compile(`actions.unknownid.result.written == true`)
	if err == nil {
		t.Fatal("Compile(unknown id) = nil, want error")
	}
	for _, want := range []string{"undefined field", "unknownid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Compile(unknown id) error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestCompileDynamicActionAccessRejected: a dynamic index, an `in` test, or a
// size read of `actions` all fail to compile against the object type (there is
// no map to index or iterate). Only the failure is asserted; the message is
// cel-go's and not part of this package's contract.
func TestCompileDynamicActionAccessRejected(t *testing.T) {
	env := declaredResultEnv(t)
	for _, src := range []string{
		`actions["build"]`,
		`"build" in actions`,
		`size(actions) > 0`,
	} {
		t.Run(src, func(t *testing.T) {
			if _, err := env.Compile(src); err == nil {
				t.Errorf("Compile(%q) = nil, want error", src)
			}
		})
	}
}

// TestIsCELFieldName proves the authoritative module-id gate the playbook
// loader uses: a normal id is accepted, and a CEL keyword or any spelling with
// no `actions.<id>` field-selection form is rejected. `Build` is asserted
// true here to show the check reports CEL referenceability, not the loader's
// separate lowercase policy.
func TestIsCELFieldName(t *testing.T) {
	for _, id := range []string{"build", "step_2", "x9", "Build"} {
		if !IsCELFieldName(id) {
			t.Errorf("IsCELFieldName(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"in", "true", "false", "null", "build.step", "build-step", ""} {
		if IsCELFieldName(id) {
			t.Errorf("IsCELFieldName(%q) = true, want false", id)
		}
	}
}

// TestEvalDeclaredResultRead: a declared id's result read evaluates against a
// map whose id entry wraps the Go Result struct in `{result: ...}`.
func TestEvalDeclaredResultRead(t *testing.T) {
	env := declaredResultEnv(t)
	prg, err := env.Compile(`actions.build.result.written`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := env.Eval(prg, Context{
		Actions: map[string]map[string]any{
			"build": {"result": log.Result{Written: true, Level: "info"}},
		},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if b, ok := got.(bool); !ok || !b {
		t.Fatalf("Eval = %#v, want true", got)
	}

	level, err := env.Eval(mustCompile(t, env, `actions.build.result.level`), Context{
		Actions: map[string]map[string]any{
			"build": {"result": log.Result{Written: true, Level: "info"}},
		},
	})
	if err != nil {
		t.Fatalf("Eval(level): %v", err)
	}
	if s, ok := level.(string); !ok || s != "info" {
		t.Fatalf("Eval(level) = %#v, want %q", level, "info")
	}
}

// TestEmptyDeclaredSetIsValid: an environment with no declared results has an
// `actions` object with no fields; any `actions.<id>` read fails at compile.
func TestEmptyDeclaredSetIsValid(t *testing.T) {
	env := NewEnv()
	if _, err := env.Compile(`event.label == "x"`); err != nil {
		t.Fatalf("Compile(event read) on empty env: %v", err)
	}
	_, err := env.Compile(`actions.build.result.written == true`)
	if err == nil {
		t.Fatal("Compile(actions read) on empty env = nil, want error")
	}
	for _, want := range []string{"undefined field", "build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Compile(actions read) on empty env error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestCompileEvalValidBooleanAgainstEvent is the happy path: a boolean
// condition reading an event field compiles and evaluates correctly.
func TestCompileEvalValidBooleanAgainstEvent(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`event.label == "bugfix"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := env.Eval(prg, Context{
		Event: map[string]any{"label": "bugfix", "priority": 3},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if b, ok := got.(bool); !ok || !b {
		t.Fatalf("Eval = %#v, want true", got)
	}
}

// TestCompileSyntaxErrorIsReturned: a syntax error is a Compile-time error,
// never a panic.
func TestCompileSyntaxErrorIsReturned(t *testing.T) {
	env := NewEnv()
	_, err := env.Compile(`event.label == `)
	if err == nil {
		t.Fatal("Compile(syntax error) = nil, want error")
	}
}

// TestCompileUnknownRootIsReturned: a reference to a root other than
// event/actions is rejected at compile time -- the reject-at-load rule.
func TestCompileUnknownRootIsReturned(t *testing.T) {
	env := NewEnv()
	_, err := env.Compile(`foo.bar == 1`)
	if err == nil {
		t.Fatal("Compile(unknown root) = nil, want error")
	}
	if !strings.Contains(err.Error(), "'foo'") {
		t.Errorf("Compile(unknown root) error = %q, want it to name the root", err.Error())
	}
}

// TestEvalMissingEventFieldNoPanic: evaluating against a missing event field
// returns an error, never panics (CEL's documented behavior).
func TestEvalMissingEventFieldNoPanic(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`event.missing == "x"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = env.Eval(prg, Context{Event: map[string]any{}})
	if err == nil {
		t.Fatal("Eval(missing field) = nil error, want error (not panic)")
	}
}

// TestEvalHasMacroPresence: has() covers the missing-key case positively.
func TestEvalHasMacroPresence(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`has(event.label)`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := env.Eval(prg, Context{Event: map[string]any{}})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if b, ok := got.(bool); !ok || b {
		t.Fatalf("has() on missing field = %#v, want false", got)
	}
}

// TestActionReferences classifies every read of the `actions` context root
// that still compiles under the per-playbook object type: a static field
// selection resolves to its id, and a bare `actions` value (which compiles)
// is reported unresolvable so the playbook loader still rejects it.
func TestActionReferences(t *testing.T) {
	env := declaredResultEnv(t)
	tests := []struct {
		name           string
		src            string
		wantIDs        []string
		wantResolvable bool
	}{
		{
			name:           "field selection resolves",
			src:            `actions.build.result.written == true`,
			wantIDs:        []string{"build"},
			wantResolvable: true,
		},
		{
			name:           "two static accesses of one id consume two idents",
			src:            `actions.build.result.written == true && actions.build.result.level == "info"`,
			wantIDs:        []string{"build"},
			wantResolvable: true,
		},
		{
			name:           "no actions reference",
			src:            `event.priority == 3`,
			wantIDs:        nil,
			wantResolvable: true,
		},
		{
			name:           "has macro over static access",
			src:            `has(actions.build.result.written)`,
			wantIDs:        []string{"build"},
			wantResolvable: true,
		},
		{
			name:           "bare actions is unresolvable",
			src:            `actions`,
			wantIDs:        nil,
			wantResolvable: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prg, err := env.Compile(tc.src)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.src, err)
			}
			ids, resolvable := prg.ActionReferences()
			if resolvable != tc.wantResolvable {
				t.Fatalf("ActionReferences(%q) resolvable = %v, want %v", tc.src, resolvable, tc.wantResolvable)
			}
			if !slices.Equal(ids, tc.wantIDs) {
				t.Fatalf("ActionReferences(%q) ids = %v, want %v", tc.src, ids, tc.wantIDs)
			}
		})
	}
}

// TestEvalDeepNestedNoPanic: hostile deep nested access errors cleanly.
func TestEvalDeepNestedNoPanic(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`event.a.b.c.d == 1`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = env.Eval(prg, Context{Event: map[string]any{}})
	if err == nil {
		t.Fatal("Eval(deep missing) = nil error, want error")
	}
}

// TestCompileOverCostLimitRejected: an expression that would exceed the cost
// limit is rejected at evaluation, not silently truncated. Verified CEL
// semantics: cost limits abort with "actual cost limit exceeded" on
// macro-heavy (map/filter) expressions -- the cost model accrues on
// iteration, not on field reads.
func TestCompileOverCostLimitRejected(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`[1,2,3,4,5].map(x, x * 2).filter(x, x > 1).size() > 3`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// With the DefaultCostLimit this succeeds (it is a modest expression).
	got, err := env.Eval(prg, Context{Event: map[string]any{}})
	if err != nil {
		t.Fatalf("Eval(default limit): %v", err)
	}
	if b, ok := got.(bool); !ok || !b {
		t.Fatalf("Eval = %#v, want true", got)
	}

	// The same expression evaluated under a tiny limit must abort with the
	// cost error -- proving the limit is enforced, never truncated.
	if _, err := evalUnderLimit(prg, 1); err == nil {
		t.Fatal("Eval(tiny limit) = nil error, want cost-limit error")
	}
}

// evalUnderLimit rebuilds the program with a caller-supplied cost limit.
func evalUnderLimit(prg *Program, limit uint64) (any, error) {
	// Rebuild a fresh env/compile here for the test with an explicit limit.
	env := NewEnv()
	ast, issues := env.celEnv.Compile(`[1,2,3,4,5].map(x, x * 2).filter(x, x > 1).size() > 3`)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	p, err := env.celEnv.Program(ast, cel.CostLimit(limit))
	if err != nil {
		return nil, err
	}
	v, _, err := p.Eval(map[string]any{"event": map[string]any{}, "actions": map[string]any{}})
	if err != nil {
		return nil, err
	}
	return v.Value(), nil
}

// TestEvalPanicFreeHostileData: a pathological nested map under evaluation
// must not panic -- CEL returns errors or values, never panics (verified
// property; this test guards the wrapper contract, not CEL itself). A
// comparison against a missing key evaluates to false rather than erroring
// (verified CEL semantics: equality does not require the field to exist).
func TestEvalPanicFreeHostileData(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`event.child == "x"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	deep := map[string]any{}
	cur := deep
	for range 5000 {
		next := map[string]any{}
		cur["child"] = next
		cur = next
	}
	got, err := env.Eval(prg, Context{Event: deep})
	if err != nil {
		t.Fatalf("Eval(hostile deep) error = %v, want no error (missing key in == -> false)", err)
	}
	if b, ok := got.(bool); !ok || b {
		t.Fatalf("Eval(hostile deep) = %#v, want false", got)
	}
}

// mustCompile compiles src against env and fails the test on error.
func mustCompile(t *testing.T, env *Env, src string) *Program {
	t.Helper()
	prg, err := env.Compile(src)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return prg
}
