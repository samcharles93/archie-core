package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/ai-sdk/agentloop"
	"github.com/samcharles93/ai-sdk/runtime"
)

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
	got, err := runner.Run(context.Background(), "/workspace", req)
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
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}
