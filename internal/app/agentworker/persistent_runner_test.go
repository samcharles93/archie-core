package agentworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

type persistentRunnerFunc func(context.Context, string, agentexec.Request, agentexec.ToolCallReporter) (agentexec.Result, error)

func (f persistentRunnerFunc) Run(ctx context.Context, workspace string, request agentexec.Request, report agentexec.ToolCallReporter) (agentexec.Result, error) {
	return f(ctx, workspace, request, report)
}

func TestPersistentRunnerRecordsStageMemoryAndPluginOverride(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "check.go"), []byte("override"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := persistentRunner{
		Runner: persistentRunnerFunc(func(_ context.Context, _ string, request agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
			if got := request.Plugins[0].Src; got != "override" {
				t.Fatalf("plugin source = %q, want persistent override", got)
			}
			return agentexec.Result{Version: agentexec.ProtocolVersion, TaskID: request.TaskID, Attempt: request.Attempt, Stage: request.Stage, Status: agentexec.StatusPassed, AppendedNotes: []string{"remember me"}}, nil
		}),
		enabled:     true,
		sessionPath: filepath.Join(dir, "session.jsonl"),
		memoryPath:  filepath.Join(dir, "memory.jsonl"),
		pluginsDir:  pluginsDir,
	}
	_, err := runner.Run(context.Background(), t.TempDir(), agentexec.Request{
		TaskID: 7, Attempt: 2, Stage: "implement",
		Plugins: []agentexec.PluginSpec{{Name: "check", Src: "bundled"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{runner.sessionPath, runner.memoryPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(content), "\n") {
			t.Errorf("%s is not newline-terminated JSONL", path)
		}
	}
	memory, err := os.ReadFile(runner.memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(memory), `"content":"remember me"`) {
		t.Errorf("memory record = %s, want appended note", memory)
	}
}

func TestReadProjectMemoryRestoresEarlierNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	if err := os.WriteFile(path, []byte("{\"content\":\"first fact\"}\n{\"content\":\"second fact\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readProjectMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"first fact", "second fact"} {
		if !strings.Contains(got, fact) {
			t.Errorf("memory prompt %q missing %q", got, fact)
		}
	}
}

func TestReadProjectMemoryAcceptsLargePersistedNote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.jsonl")
	note := strings.Repeat("x", 70*1024)
	encoded := `{"content":"` + note + `"}` + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readProjectMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, note) {
		t.Error("large persisted note was not restored")
	}
}

func TestPersistentRunnerRejectsFailedResultBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	runner := persistentRunner{
		Runner: persistentRunnerFunc(func(_ context.Context, _ string, _ agentexec.Request, _ agentexec.ToolCallReporter) (agentexec.Result, error) {
			return agentexec.Result{AppendedNotes: []string{"untrusted"}}, os.ErrPermission
		}),
		enabled:     true,
		sessionPath: filepath.Join(dir, "session.jsonl"),
		memoryPath:  filepath.Join(dir, "memory.jsonl"),
		pluginsDir:  filepath.Join(dir, "plugins"),
	}
	_, err := runner.Run(context.Background(), t.TempDir(), agentexec.Request{}, nil)
	if err == nil {
		t.Fatal("expected runner error")
	}
	for _, path := range []string{runner.sessionPath, runner.memoryPath} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("%s persisted rejected output", path)
		}
	}
}
