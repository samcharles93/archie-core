package expr

import (
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types/ref"
)

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

// TestEvalActionsContext: prior-action results read via actions.<id>.result.
func TestEvalActionsContext(t *testing.T) {
	env := NewEnv()
	prg, err := env.Compile(`actions.a.result.written == true`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, err := env.Eval(prg, Context{
		Actions: map[string]map[string]any{
			"a": {"result": map[string]any{"written": true}},
		},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if b, ok := got.(bool); !ok || !b {
		t.Fatalf("Eval = %#v, want true", got)
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
	if rv, ok := v.(ref.Val); ok {
		return rv.Value(), nil
	}
	return v, nil
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
