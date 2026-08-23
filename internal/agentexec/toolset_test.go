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

// TestToolSetReportsCompletedToolCalls pins archie-core-467's task-transcript
// counterpart: every tool invocation built through BuildToolSet must notify
// ToolSetOptions.OnToolCall exactly once, on both the success and failure
// path, so a workflow stage can surface it on the task timeline.
func TestToolSetReportsCompletedToolCalls(t *testing.T) {
	tests := []struct {
		name       string
		handler    tools.Handler
		wantFailed bool
		wantDetail string
	}{
		{
			name:       "success",
			handler:    func(context.Context, map[string]any) (any, error) { return "wrote 3 lines", nil },
			wantFailed: false,
			wantDetail: `"wrote 3 lines"`,
		},
		{
			name:       "failure",
			handler:    func(context.Context, map[string]any) (any, error) { return nil, errors.New("permission denied") },
			wantFailed: true,
			wantDetail: "error: permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			if err := reg.Register(tools.ToolEntry{Name: "write_file", Handler: tt.handler}); err != nil {
				t.Fatal(err)
			}

			var reports []ToolCallReport
			set := mustBuildToolSetWith(t, reg, ToolSetOptions{
				OnToolCall: func(rep ToolCallReport) { reports = append(reports, rep) },
			})
			_, _ = set["write_file"].Execute(context.Background(), `{}`)

			if len(reports) != 1 {
				t.Fatalf("OnToolCall was called %d times, want 1: %+v", len(reports), reports)
			}
			got := reports[0]
			if got.Tool != "write_file" {
				t.Errorf("Tool = %q, want %q", got.Tool, "write_file")
			}
			if got.Failed != tt.wantFailed {
				t.Errorf("Failed = %v, want %v", got.Failed, tt.wantFailed)
			}
			if got.Detail != tt.wantDetail {
				t.Errorf("Detail = %q, want %q", got.Detail, tt.wantDetail)
			}
		})
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
	payload := strings.Repeat("q", 4000)

	reg := registryReturning(t, tools.ToolEntry{Name: "big"}, payload)
	set := mustBuildToolSetWith(t, reg, ToolSetOptions{MaxResultChars: 100, SpillDir: dir})

	out, err := set["big"].Execute(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "spilled") {
		t.Fatalf("result = %q, want a spill reference", out)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("spill directory entries = %v, err = %v; want one spill", entries, err)
	}
	spillPath := filepath.Join(dir, entries[0].Name())
	if !strings.Contains(out, spillPath) {
		t.Errorf("result %q does not name the spill path %q", out, spillPath)
	}
	body, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), payload) {
		t.Error("spill file does not hold the full payload")
	}
}

// TestToolLimitsCreatesSpillDir covers the gap that made spilling dead code in
// production: the configured directory was never created, so every oversized
// result silently fell back to inline truncation.
func TestToolLimitsCreatesSpillDir(t *testing.T) {
	// A nested path proves the whole tree is created, not just a leaf.
	dir := filepath.Join(t.TempDir(), "work", "tool-spill")
	limits := ToolLimits{MaxResultChars: 100, SpillDir: dir}

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

// Tool result volume is bounded per invocation only. Repeated large results
// may each be spilled or truncated, but accumulation never stops the turn.
func TestToolSetPerResultLimitNeverStopsFurtherCalls(t *testing.T) {
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
			reg := registryReturning(t, tools.ToolEntry{Name: "big"}, strings.Repeat("q", 60_000))
			set := mustBuildToolSetWith(t, reg, ToolSetOptions{MaxResultChars: 100, SpillDir: dir})

			for range 5 {
				_, err := set["big"].Execute(context.Background(), "{}")
				if err != nil {
					t.Fatalf("later tool call was stopped by accumulated output: %v", err)
				}
			}
		})
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

// testApprover is a scripted ApprovalRequester for the tool-approval gate
// tests. It returns whatever decision and error the test wired up, so each
// test exercises one branch of the gate.
type testApprover struct {
	decision tools.ApprovalDecision
	err      error
}

func (a *testApprover) RequestApproval(context.Context, string, string) (tools.ApprovalDecision, error) {
	return a.decision, a.err
}

// approvalEntry returns a single-entry registry whose tool is marked
// RequiresApproval and records whether its handler ran.
func approvalEntry(t *testing.T, reg *tools.Registry, ran *bool) {
	t.Helper()
	if err := reg.Register(tools.ToolEntry{
		Name:           "gated",
		Description:    "requires human consent",
		Classification: tools.RequiresApproval,
		Handler: func(context.Context, map[string]any) (any, error) {
			*ran = true
			return "ran", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestToolSetApprovalApprovedExecutes(t *testing.T) {
	reg := tools.NewRegistry()
	ran := false
	approvalEntry(t, reg, &ran)

	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Approval: &testApprover{decision: tools.ApprovalApproved}})
	out, err := set["gated"].Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("handler did not run after approval was granted")
	}
	if out != `"ran"` {
		t.Errorf("output = %q, want \"ran\"", out)
	}
}

func TestToolSetApprovalDeniedReturnsError(t *testing.T) {
	reg := tools.NewRegistry()
	ran := false
	approvalEntry(t, reg, &ran)

	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Approval: &testApprover{decision: tools.ApprovalDenied}})
	_, err := set["gated"].Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected an error when approval is denied")
	}
	if !errors.Is(err, tools.ErrApprovalDenied) {
		t.Errorf("error = %v, want it to wrap ErrApprovalDenied", err)
	}
	if ran {
		t.Error("handler ran despite the denial")
	}
}

func TestToolSetApprovalPropagatesApproverError(t *testing.T) {
	reg := tools.NewRegistry()
	ran := false
	approvalEntry(t, reg, &ran)

	wantErr := errors.New("approver exploded")
	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Approval: &testApprover{err: wantErr}})
	_, err := set["gated"].Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected an error from the approver")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want it to wrap the approver error", err)
	}
	if ran {
		t.Error("handler ran despite the approver error")
	}
}

func TestToolSetApprovalMissingApproverReturnsError(t *testing.T) {
	ran := false
	entry := tools.ToolEntry{
		Name:           "gated",
		Description:    "requires human consent",
		Classification: tools.RequiresApproval,
		Handler: func(context.Context, map[string]any) (any, error) {
			ran = true
			return "should not run", nil
		},
	}
	// The build-time gate omits approval-requiring tools from a set built
	// without an approver, so exercise the runtime gate directly: the execute
	// closure must still refuse to run the tool.
	tool := aicore.NewTool(entry.Name, entry.Description, json.RawMessage(`{"type":"object","properties":{}}`), toolExecute(entry, ToolSetOptions{}))
	_, err := tool.Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected an error when no approver is configured")
	}
	if !strings.Contains(err.Error(), "no approver") {
		t.Errorf("error = %v, want it to name the missing approver", err)
	}
	if ran {
		t.Error("handler ran despite there being no approver")
	}
}

func TestToolSetWithoutApprovalFlagExecutesNormally(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:    "plain",
		Handler: func(context.Context, map[string]any) (any, error) { return "ok", nil },
	}); err != nil {
		t.Fatal(err)
	}

	// Even with an approver that would deny, a non-gated tool must run.
	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Approval: &testApprover{decision: tools.ApprovalDenied}})
	out, err := set["plain"].Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `"ok"` {
		t.Errorf("output = %q, want \"ok\"", out)
	}
}

func TestBuildToolSetOmitsApprovalToolsWithoutApprover(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:           "gated",
		Description:    "requires human consent",
		Classification: tools.RequiresApproval,
		Handler:        func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(tools.ToolEntry{
		Name:    "plain",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSetWith(t, reg, ToolSetOptions{})
	if _, ok := set["gated"]; ok {
		t.Error("ToolSet included an approval-requiring tool with no approver")
	}
	if _, ok := set["plain"]; !ok {
		t.Error("ToolSet dropped a non-approval tool")
	}
}

func TestBuildToolSetIncludesApprovalToolsWithApprover(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(tools.ToolEntry{
		Name:           "gated",
		Description:    "requires human consent",
		Classification: tools.RequiresApproval,
		Handler:        func(context.Context, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}

	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Approval: &testApprover{decision: tools.ApprovalApproved}})
	if _, ok := set["gated"]; !ok {
		t.Error("ToolSet omitted an approval-requiring tool with an approver configured")
	}
}

func TestToolSetApprovalFailsClosedOnUnknownDecision(t *testing.T) {
	reg := tools.NewRegistry()
	ran := false
	approvalEntry(t, reg, &ran)

	// ApprovalDecision 999 is not a recognised constant. The gate must
	// fail closed: an unknown value from a faulty approver must not
	// execute the tool.
	unknownDecision := tools.ApprovalDecision(999)
	set := mustBuildToolSetWith(t, reg, ToolSetOptions{Approval: &testApprover{decision: unknownDecision}})
	tool, ok := set["gated"]
	if !ok {
		t.Fatal("tool not in set")
	}
	_, err := tool.Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error for unknown decision, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected decision") {
		t.Errorf("error = %v, want 'unexpected decision'", err)
	}
	if ran {
		t.Error("tool executed despite unknown approval decision")
	}
}
