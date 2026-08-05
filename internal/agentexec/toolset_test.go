package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aicore "github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/tools"
)

func TestToolSetConvertsRegisteredEntries(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:        "echo",
		Description: "echoes its input",
		Schema:      tools.JSONSchema{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			return input["msg"], nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)

	tool, ok := set["echo"]
	if !ok {
		t.Fatal("ToolSet did not include the \"echo\" entry")
	}
	if tool.Name != "echo" || tool.Description != "echoes its input" {
		t.Errorf("tool = %+v", tool)
	}
	if len(tool.Parameters) == 0 {
		t.Error("Parameters is empty, want the marshaled schema")
	}
}

func TestToolSetHandlerRoundTripsJSON(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name: "add",
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			a, _ := input["a"].(float64)
			b, _ := input["b"].(float64)
			return a + b, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	out, err := set["add"].Execute(context.Background(), `{"a": 2, "b": 3}`)
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q did not decode as JSON: %v", out, err)
	}
	if got != 5 {
		t.Errorf("result = %v, want 5", got)
	}
}

func TestToolSetHandlerPropagatesHandlerError(t *testing.T) {
	reg := tools.NewRegistry()
	wantErr := errors.New("boom")
	if err := reg.Register(tools.ToolEntry{
		Name: "fails",
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, wantErr
		},
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	_, err := set["fails"].Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error from failing handler")
	}
}

func TestToolSetHandlerRejectsMalformedInput(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "noop",
		Handler: func(context.Context, map[string]any) (any, error) { return "ok", nil },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	_, err := set["noop"].Execute(context.Background(), `not json`)
	if err == nil {
		t.Fatal("expected error decoding malformed input JSON")
	}
}

func TestToolSetOmitsUnavailableTools(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "offline",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
		CheckFn: func() bool { return false },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	if _, ok := set["offline"]; ok {
		t.Error("ToolSet included a tool whose CheckFn reports unavailable")
	}
}

func TestToolSetEmptyRegistryReturnsEmptySet(t *testing.T) {
	set := mustBuildToolSet(t, tools.NewRegistry())
	if len(set) != 0 {
		t.Errorf("len(set) = %d, want 0", len(set))
	}
}

func TestToolSetNilRegistryReturnsEmptySet(t *testing.T) {
	set := mustBuildToolSet(t, nil)
	if len(set) != 0 {
		t.Errorf("len(set) = %d, want 0 for nil registry", len(set))
	}
}

func TestToolSetUsesObjectSchemaForParameterlessTools(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "no_args",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}

	parameters := mustBuildToolSet(t, reg)["no_args"].Parameters
	var schema map[string]any
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatalf("Parameters = %s, want JSON object: %v", parameters, err)
	}
	if schema["type"] != "object" {
		t.Fatalf("Parameters = %s, want object schema", parameters)
	}
}

func TestToolSetHandlerReturningNilOutputProducesNullJSON(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "voidy",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSet(t, reg)
	out, err := set["voidy"].Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "null" {
		t.Errorf("output = %q, want \"null\"", out)
	}
}

func TestBuildToolSetReportsInvalidDynamicSchemas(t *testing.T) {
	tests := []struct {
		name  string
		entry tools.ToolEntry
	}{
		{
			name: "schema override panic",
			entry: tools.ToolEntry{
				Name:    "panics",
				Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
				DynamicSchemaOverrides: func(tools.JSONSchema) tools.JSONSchema {
					panic("schema panic")
				},
			},
		},
		{
			name: "unmarshalable schema",
			entry: tools.ToolEntry{
				Name:    "invalid",
				Schema:  tools.JSONSchema{"bad": func() {}},
				Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
			},
		},
		{
			name: "availability panic",
			entry: tools.ToolEntry{
				Name:    "unavailable",
				Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
				CheckFn: func() bool {
					panic("availability panic")
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := tools.NewRegistry()
			if err := registry.Register(tt.entry); err != nil {
				t.Fatal(err)
			}
			var (
				err      error
				panicked any
			)
			func() {
				defer func() { panicked = recover() }()
				_, err = BuildToolSet(registry, ToolSetOptions{})
			}()
			if panicked != nil {
				t.Fatalf("BuildToolSet() panicked: %v", panicked)
			}
			if err == nil {
				t.Fatal("BuildToolSet() error = nil, want invalid schema error")
			}
		})
	}
}

func mustBuildToolSet(t *testing.T, registry *tools.Registry) map[string]*aicore.Tool {
	t.Helper()
	return mustBuildToolSetWith(t, registry, ToolSetOptions{})
}

func mustBuildToolSetWith(t *testing.T, registry *tools.Registry, opts ToolSetOptions) map[string]*aicore.Tool {
	t.Helper()
	set, err := BuildToolSet(registry, opts)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// registryReturning builds a single-entry registry whose tool returns payload.
func registryReturning(t *testing.T, entry tools.ToolEntry, payload string) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	entry.Handler = func(context.Context, map[string]any) (any, error) { return payload, nil }
	if err := reg.Register(entry); err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestToolSetCapsResults covers the limit that decides how much of a tool
// result reaches the model. The workspace tools truncate themselves, so the
// entries that matter here are the ones that do not: skill bodies and MCP
// results were previously handed over whole.
func TestToolSetCapsResults(t *testing.T) {
	const payloadSize = 4000
	payload := strings.Repeat("z", payloadSize)

	tests := []struct {
		name          string
		entryLimit    int // ToolEntry.MaxResultSizeChars
		defaultLimit  int // ToolSetOptions.MaxResultChars
		wantTruncated bool
	}{
		{name: "under the default passes through", defaultLimit: payloadSize * 2},
		{name: "over the default is capped", defaultLimit: 100, wantTruncated: true},
		{name: "per-entry limit overrides a larger default", entryLimit: 100, defaultLimit: payloadSize * 2, wantTruncated: true},
		{name: "per-entry limit overrides a smaller default", entryLimit: payloadSize * 2, defaultLimit: 100},
		{name: "negative default disables capping", defaultLimit: -1},
		{name: "zero default disables capping", defaultLimit: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := registryReturning(t, tools.ToolEntry{
				Name:               "big",
				MaxResultSizeChars: tc.entryLimit,
			}, payload)

			set := mustBuildToolSetWith(t, reg, ToolSetOptions{MaxResultChars: tc.defaultLimit})
			out, err := set["big"].Execute(context.Background(), "{}")
			if err != nil {
				t.Fatal(err)
			}

			truncated := strings.Contains(out, "truncated")
			if truncated != tc.wantTruncated {
				t.Fatalf("truncated = %v, want %v (result %d bytes)", truncated, tc.wantTruncated, len(out))
			}
			if !tc.wantTruncated && len(out) < payloadSize {
				t.Errorf("result is %d bytes, want the whole %d-byte payload", len(out), payloadSize)
			}
		})
	}
}

// TestToolSetSpillsOversizeResults proves an oversized result is displaced to
// disk rather than lost: the model gets a path it can read back with the read
// tool, which is the whole point of spilling instead of truncating.
func TestToolSetSpillsOversizeResults(t *testing.T) {
	dir := t.TempDir()
	budget := tools.NewTurnBudget(1_000_000, dir)
	payload := strings.Repeat("q", 4000)

	reg := registryReturning(t, tools.ToolEntry{Name: "big"}, payload)
	set := mustBuildToolSetWith(t, reg, ToolSetOptions{MaxResultChars: 100, Budget: budget})

	out, err := set["big"].Execute(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "spilled") {
		t.Fatalf("result = %q, want a spill reference", out)
	}

	spilled := budget.Spilled()
	if len(spilled) != 1 || spilled[0].Path == "" {
		t.Fatalf("Spilled() = %+v, want one spill with a path", spilled)
	}
	if !strings.Contains(out, spilled[0].Path) {
		t.Errorf("result %q does not name the spill path %q", out, spilled[0].Path)
	}
	body, err := os.ReadFile(spilled[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), payload) {
		t.Error("spill file does not hold the full payload")
	}
}

// TestToolLimitsCreatesSpillDir covers the gap that made spilling dead code in
// production: the configured directory defaults to <work_dir>/tool-spill and
// nothing ever created it, while TurnBudget.Spill swallows the write error. So
// every oversized result silently fell back to inline truncation and the spill
// path was never exercised outside tests that made their own directory.
func TestToolLimitsCreatesSpillDir(t *testing.T) {
	// A nested path proves the whole tree is created, not just a leaf.
	dir := filepath.Join(t.TempDir(), "work", "tool-spill")
	limits := ToolLimits{MaxResultChars: 100, TurnBudgetChars: 1_000_000, SpillDir: dir}

	if err := limits.EnsureSpillDir(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("spill directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}

	// And a spill actually lands there once it exists.
	reg := registryReturning(t, tools.ToolEntry{Name: "big"}, strings.Repeat("z", 5000))
	set := mustBuildToolSetWith(t, reg, limits.Options())
	if _, err := set["big"].Execute(context.Background(), "{}"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("no spill file was written into the created directory")
	}
}

// TestToolLimitsEnsureSpillDirNoop covers the disabled case: no directory
// configured is not an error, it selects inline truncation.
func TestToolLimitsEnsureSpillDirNoop(t *testing.T) {
	if err := (ToolLimits{}).EnsureSpillDir(); err != nil {
		t.Errorf("EnsureSpillDir() with no directory = %v, want nil", err)
	}
}

// TestToolSetChargesBudgetOnce pins the invariant that makes the turn budget
// mean anything: a turn is charged exactly the bytes it was shown.
//
// Displacing an oversized result to disk used to charge the full displaced
// size AND the short reference handed back, so two 60 KB results burned more
// than the 200 K default budget while the model saw about 200 characters.
func TestToolSetChargesBudgetOnce(t *testing.T) {
	tests := []struct {
		name     string
		spillDir bool
	}{
		{name: "results spilled to disk", spillDir: true},
		{name: "results truncated inline", spillDir: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := ""
			if tc.spillDir {
				dir = t.TempDir()
			}
			budget := tools.NewTurnBudget(1_000_000, dir)
			reg := registryReturning(t, tools.ToolEntry{Name: "big"}, strings.Repeat("q", 60_000))
			set := mustBuildToolSetWith(t, reg, ToolSetOptions{MaxResultChars: 100, Budget: budget})

			delivered := 0
			for range 2 {
				out, err := set["big"].Execute(context.Background(), "{}")
				if err != nil {
					t.Fatal(err)
				}
				delivered += len(out)
			}

			if used := budget.Used(); used != delivered {
				t.Errorf("budget charged %d for %d delivered characters", used, delivered)
			}
		})
	}
}

// TestToolSetBudgetStopsFurtherCalls checks the aggregate cap: once a turn has
// spent its budget the next tool call must fail loudly, so the model is told it
// has run out rather than silently receiving nothing.
func TestToolSetBudgetStopsFurtherCalls(t *testing.T) {
	budget := tools.NewTurnBudget(50, "")
	reg := registryReturning(t, tools.ToolEntry{Name: "chatty"}, strings.Repeat("w", 200))
	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Budget: budget})

	if _, err := set["chatty"].Execute(context.Background(), "{}"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !budget.Exceeded() {
		t.Fatal("budget should be exceeded after a 200-byte result against a 50-byte cap")
	}
	_, err := set["chatty"].Execute(context.Background(), "{}")
	if err == nil {
		t.Fatal("second call succeeded, want a budget-exceeded error")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("error = %v, want it to name the budget", err)
	}
}

// TestToolSetExcludesToolsets covers the task-agent case: a task runs in its
// own worktree, so the chat workspace's file and shell tools must not follow it
// in and bypass the agent loop's read-only and protected-path enforcement.
func TestToolSetExcludesToolsets(t *testing.T) {
	reg := tools.NewRegistry()
	for _, e := range []tools.ToolEntry{
		{Name: "shell", Toolset: "workspace"},
		{Name: "memory_edit", Toolset: "memory"},
	} {
		e.Handler = func(context.Context, map[string]any) (any, error) { return "ok", nil }
		if err := reg.Register(e); err != nil {
			t.Fatal(err)
		}
	}

	full := mustBuildToolSetWith(t, reg, ToolSetOptions{})
	if len(full) != 2 {
		t.Fatalf("with no exclusions the set has %d tools, want 2", len(full))
	}

	limited := mustBuildToolSetWith(t, reg, ToolSetOptions{ExcludeToolsets: []string{"workspace"}})
	if _, ok := limited["shell"]; ok {
		t.Error("workspace tool survived the exclusion")
	}
	if _, ok := limited["memory_edit"]; !ok {
		t.Error("memory tool was dropped, want only the workspace toolset excluded")
	}
}
