package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteTool_PopulatesDiffDetails_NewFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	params := `{"path": "f.txt", "content": "hello\n"}`
	res, err := tool.Execute(context.Background(), json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}

	details, ok := res.Details.(DiffDetails)
	if !ok {
		t.Fatalf("expected Details to be a DiffDetails, got %T", res.Details)
	}
	if details.OldContent != "" {
		t.Fatalf("expected empty OldContent for a new file, got %q", details.OldContent)
	}
	if details.NewContent != "hello\n" {
		t.Fatalf("NewContent mismatch: got %q", details.NewContent)
	}
	if details.Path != path {
		t.Fatalf("Path mismatch: got %q want %q", details.Path, path)
	}
}

func TestWriteTool_PopulatesDiffDetails_Overwrite(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "old content\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	params := `{"path": "f.txt", "content": "new content\n", "overwrite": true}`
	res, err := tool.Execute(context.Background(), json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}

	details, ok := res.Details.(DiffDetails)
	if !ok {
		t.Fatalf("expected Details to be a DiffDetails, got %T", res.Details)
	}
	if details.OldContent != original {
		t.Fatalf("OldContent mismatch: got %q want %q", details.OldContent, original)
	}
	if details.NewContent != "new content\n" {
		t.Fatalf("NewContent mismatch: got %q", details.NewContent)
	}
}

// TestWriteTool_RespectsParentContextDeadline is the regression test for
// tau-6wa: the executor used to create a timeout-derived context and then
// discard it, passing the original (unbounded) ctx to every downstream I/O
// call. With that bug, an already-expired parent context would have no
// effect on the write. Here the parent's deadline has already passed when
// Execute is called, so the executor must surface a timeout error instead
// of proceeding to write the file.
func TestWriteTool_RespectsParentContextDeadline(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	params := `{"path": "f.txt", "content": "hello\n"}`
	res, err := tool.Execute(ctx, json.RawMessage(params), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a timeout error result for an already-expired context, got success: %+v", res)
	}

	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("expected no file to be written when the context had already expired")
	}
}

// TestMutationToolsLeaveNoUnrestorableCheckpoints guards the decision that the
// workspace mutation tools carry no rollback state. A pre-mutation checkpoint
// writer whose only reader (CheckpointStore.RestoreLatest) has no production
// caller, and whose store is never pruned, is dead weight and grows the
// workspace without bound. If the inert writer is reintroduced without a
// reachable restore caller, this fails.
func TestMutationToolsLeaveNoUnrestorableCheckpoints(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	write := NewWriteTool(tmp, NewMutationQueue(), nil)
	if res, err := write.Execute(context.Background(), json.RawMessage(`{"path":"g.txt","content":"new\n"}`), nil); err != nil || res.IsError {
		t.Fatalf("write: err=%v res=%+v", err, res)
	}

	edit := NewEditTool(tmp, NewMutationQueue(), nil)
	if res, err := edit.Execute(context.Background(), json.RawMessage(`{"path":"f.txt","edits":[{"old_text":"hello","new_text":"hi"}]}`), nil); err != nil || res.IsError {
		t.Fatalf("edit: err=%v res=%+v", err, res)
	}

	store := filepath.Join(tmp, ".git", "archie-checkpoints")
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("mutating tools left unrestorable checkpoint data at %s: stat err = %v", store, err)
	}
}
