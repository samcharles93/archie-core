package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/ai-sdk/agentloop"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/tools"
)

func TestPluginToolSetExecutesBundledPlugin(t *testing.T) {
	set, err := pluginToolSet(Request{Plugins: []PluginSpec{{Name: "echo", Src: "package main\nfunc Run(input string) string { return input + \"!\" }"}}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := set["echo"]
	got, err := tool.Execute(context.Background(), `{"input":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello!" {
		t.Errorf("plugin output = %q, want hello!", got)
	}
}

func TestPluginToolSetExcludedFromConstrainedStages(t *testing.T) {
	requests := []Request{
		{ReadOnly: true, Plugins: []PluginSpec{{Name: "unsafe"}}},
		{Protection: Protection{Suffixes: []string{"_test.go"}}, Plugins: []PluginSpec{{Name: "unsafe"}}},
		{Gate: Gate{Commands: []Command{{Name: "test"}}}, Plugins: []PluginSpec{{Name: "unsafe"}}},
	}
	for _, request := range requests {
		set, err := pluginToolSet(request, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(set) != 0 {
			t.Error("plugin tool exposed in constrained stage")
		}
	}
}

func TestPluginToolSetRejectsAgentLoopCollisions(t *testing.T) {
	for _, name := range []string{"write", "finish"} {
		_, err := pluginToolSet(Request{Plugins: []PluginSpec{{Name: name}}}, t.TempDir())
		if err == nil {
			t.Errorf("plugin %q did not conflict with agent-loop tool", name)
		}
	}
}

func TestInProcessRunnerMapsRequestAndCapturesOutput(t *testing.T) {
	runner := &InProcessRunner{
		runtime: runtime.NewRuntime(runtime.Config{}),
		run: func(ctx context.Context, cfg agentloop.Config) (agentloop.Result, error) {
			if cfg.WorkDir != "/workspace" || cfg.ModelRef != "provider/model" || cfg.Mission != "mission" {
				t.Fatalf("unexpected config: workdir=%q model=%q mission=%q", cfg.WorkDir, cfg.ModelRef, cfg.Mission)
			}
			if cfg.ProtectPaths == nil || !cfg.ProtectPaths("nested/file_test.go") || !cfg.ProtectPaths("view_templ.go") {
				t.Fatal("declarative path protection was not applied")
			}
			if err := cfg.Notes.Append(ctx, "verified note"); err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.Extra["decide"].Execute(ctx, `{"fit":true}`); err != nil {
				t.Fatal(err)
			}
			return agentloop.Result{Status: agentloop.StatusPassed, Summary: "done", TokensUsed: 10}, nil
		},
	}
	req := Request{
		Version: ProtocolVersion, TaskID: 1, Attempt: 1, Stage: "assess", Model: "provider/model", Mission: "mission",
		Protection:   Protection{Suffixes: []string{"_templ.go"}, Globs: []string{"*_test.go"}},
		CaptureTools: []CaptureTool{{Name: "decide", Parameters: json.RawMessage(`{"type":"object"}`), MaxCalls: 1}},
	}
	got, err := runner.Run(context.Background(), "/workspace", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPassed || got.Summary != "done" || got.TokensUsed != 10 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if len(got.AppendedNotes) != 1 || got.AppendedNotes[0] != "verified note" {
		t.Fatalf("appended notes = %v", got.AppendedNotes)
	}
	if len(got.Captures["decide"]) != 1 || string(got.Captures["decide"][0]) != `{"fit":true}` {
		t.Fatalf("captures = %v", got.Captures)
	}
}

func TestInProcessRunnerIncludesAvailableCentralTools(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ToolEntry{
		Name: "memory_search",
		Handler: func(context.Context, map[string]any) (any, error) {
			return "found", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &InProcessRunner{
		runtime: runtime.NewRuntime(runtime.Config{}),
		tools:   registry,
		run: func(ctx context.Context, cfg agentloop.Config) (agentloop.Result, error) {
			tool, ok := cfg.Extra["memory_search"]
			if !ok {
				t.Fatal("central tool is not present in agentloop Config.Extra")
			}
			got, err := tool.Execute(ctx, `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if got != `"found"` {
				t.Fatalf("tool output = %q, want JSON string", got)
			}
			return agentloop.Result{Status: agentloop.StatusPassed}, nil
		},
	}
	_, err := runner.Run(context.Background(), "/workspace", Request{
		Version: ProtocolVersion,
		TaskID:  1,
		Attempt: 1,
		Stage:   "build",
		Model:   "provider/model",
		Mission: "mission",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInProcessRunnerRejectsInvalidCentralToolSchemas(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ToolEntry{
		Name:    "invalid",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
		DynamicSchemaOverrides: func(tools.JSONSchema) tools.JSONSchema {
			panic("schema panic")
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &InProcessRunner{
		runtime: runtime.NewRuntime(runtime.Config{}),
		tools:   registry,
		run: func(context.Context, agentloop.Config) (agentloop.Result, error) {
			t.Fatal("agent loop ran with an invalid central tool schema")
			return agentloop.Result{}, nil
		},
	}
	_, err := runner.Run(context.Background(), "/workspace", Request{
		Version: ProtocolVersion,
		TaskID:  1,
		Attempt: 1,
		Stage:   "build",
		Model:   "provider/model",
		Mission: "mission",
	}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want invalid central tool schema error")
	}
}

func TestProtectionMatcherAppliesBasenameAndPathGlobs(t *testing.T) {
	match := protectionMatcher(Protection{Globs: []string{"*_test.go", "tests/*.rs"}}, false)
	tests := []struct {
		path string
		want bool
	}{
		{path: "main_test.go", want: true},
		{path: "internal/foo/bar_test.go", want: true},
		{path: "tests/widget.rs", want: true},
		{path: "src/widget.rs", want: false},
	}
	for _, tt := range tests {
		if got := match(tt.path); got != tt.want {
			t.Errorf("match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestCaptureToolRejectsInvalidDataBeforeRecording(t *testing.T) {
	captures := make(map[string][]json.RawMessage)
	tool := captureToolSet([]CaptureTool{{
		Name: "decide", RequiredFields: []string{"fit", "reasons"}, NonEmptyStrings: []string{"reasons"},
		BooleanFields: []string{"fit"},
	}}, captures)["decide"]
	output, err := tool.Execute(context.Background(), `{"fit":true,"reasons":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures["decide"]) != 0 || output != "decide rejected: reasons must be a non-empty string" {
		t.Fatalf("invalid capture output=%q captures=%v", output, captures)
	}
}

func TestCaptureToolRejectsWrongFieldTypeBeforeRecording(t *testing.T) {
	captures := make(map[string][]json.RawMessage)
	tool := captureToolSet([]CaptureTool{{Name: "decide", BooleanFields: []string{"fit"}}}, captures)["decide"]
	output, err := tool.Execute(context.Background(), `{"fit":"true"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(captures["decide"]) != 0 || output != "decide rejected: fit must be a boolean" {
		t.Fatalf("invalid capture output=%q captures=%v", output, captures)
	}
}

func TestScriptToolRunsAGoScriptAndReturnsItsOutput(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi from a script\") }\n"
	if err := os.WriteFile(filepath.Join(workspace, "scripts", "hello.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := scriptToolSet(workspace)["run_go_script"]
	out, err := tool.Execute(context.Background(), `{"path":"scripts/hello.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi from a script\n" {
		t.Fatalf("run_go_script output = %q", out)
	}
}

func TestScriptToolRejectsPathsEscapingTheWorkspace(t *testing.T) {
	workspace := t.TempDir()
	tool := scriptToolSet(workspace)["run_go_script"]
	out, err := tool.Execute(context.Background(), `{"path":"../outside.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "run_go_script rejected: path escapes the workspace" {
		t.Fatalf("run_go_script output = %q, want a workspace-escape rejection", out)
	}
}

func TestScriptToolRejectsMissingPath(t *testing.T) {
	tool := scriptToolSet(t.TempDir())["run_go_script"]
	out, err := tool.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "run_go_script rejected: arguments must be a JSON object with a non-empty path field" {
		t.Fatalf("run_go_script output = %q", out)
	}
}

func TestMergeToolSetsLaterWinsOnCollision(t *testing.T) {
	first := scriptToolSet(t.TempDir())
	captures := make(map[string][]json.RawMessage)
	second := captureToolSet([]CaptureTool{{Name: "run_go_script"}}, captures)
	merged := mergeToolSets(first, second)
	if len(merged) != 1 {
		t.Fatalf("merged = %v, want exactly one tool", merged)
	}
	if _, err := merged["run_go_script"].Execute(context.Background(), `{}`); err != nil {
		t.Fatal(err)
	}
	if len(captures["run_go_script"]) != 1 {
		t.Fatalf("expected the capture tool (second set) to win, captures = %v", captures)
	}
}

func TestInProcessRunnerPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &InProcessRunner{
		runtime: runtime.NewRuntime(runtime.Config{}),
		run: func(context.Context, agentloop.Config) (agentloop.Result, error) {
			return agentloop.Result{Status: agentloop.StatusParked, StopReason: agentloop.StopTimedOut}, nil
		},
	}
	_, err := runner.Run(ctx, "/workspace", Request{
		Version: ProtocolVersion, TaskID: 1, Attempt: 1, Stage: "build", Model: "provider/model", Mission: "mission",
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

// TestScriptToolHonorsContextCancellation proves that the run_go_script
// tool handler discards the incoming context (the handler is declared as
// func(_ context.Context, input string) and calls skillscript.Run without
// forwarding ctx). Every other execution path in archie-core respects
// context cancellation (SubprocessRunner uses exec.CommandContext,
// InProcessRunner checks context.Cause), but a cancelled/timed-out context
// has zero effect on run_go_script — a blocking script wedges the
// goroutine permanently.
func TestScriptToolHonorsContextCancellation(t *testing.T) {
	workspace := t.TempDir()
	src := `package main

func main() {
	select {}
}
`
	if err := os.WriteFile(filepath.Join(workspace, "blocker.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled — the tool should return promptly

	tool := scriptToolSet(workspace)["run_go_script"]

	done := make(chan struct{})
	go func() {
		// The result is irrelevant here -- this goroutine only exists to
		// prove Execute returns promptly once ctx is cancelled.
		_, _ = tool.Execute(ctx, `{"path":"blocker.go"}`)
		close(done)
	}()

	select {
	case <-done:
		// After the fix, the tool returns quickly because the cancelled
		// context propagates through to skillscript.Run, which selects
		// on ctx.Done() and abandons the blocking EvalPath goroutine.
		t.Log("run_go_script returned promptly (context was honored)")
	case <-time.After(3 * time.Second):
		t.Error("run_go_script ignores context cancellation: " +
			"the handler discards its context parameter (declared as _) " +
			"and calls skillscript.Run without forwarding it (issue #45)")
	}
}
