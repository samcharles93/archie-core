package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A write takes a checkpoint of the file it is about to replace, and that
// checkpoint puts the original bytes back.
func TestWriteTool_CheckpointRestoresOriginal(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "original\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"f.txt","content":"changed\n","overwrite":true}`,
	), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}

	store := NewCheckpointStore(tmp)
	if _, err := store.RestoreLatest(path); err != nil {
		t.Fatalf("RestoreLatest: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("restored content = %q, want %q", got, original)
	}
}

// A checkpoint taken before creating a file restores by removing it: the
// recorded pre-state is "did not exist".
func TestWriteTool_CheckpointOfNewFileRestoresAbsence(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "created.txt")

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"created.txt","content":"new\n"}`,
	), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}

	store := NewCheckpointStore(tmp)
	if _, err := store.RestoreLatest(path); err != nil {
		t.Fatalf("RestoreLatest: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("expected restore to remove the created file, stat err = %v", statErr)
	}
}

// Edit is a mutating tool too, and its checkpoint restores the original.
func TestEditTool_CheckpointRestoresOriginal(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(tmp, NewMutationQueue(), nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"f.txt","edits":[{"old_text":"two","new_text":"TWO"}]}`,
	), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}

	store := NewCheckpointStore(tmp)
	if _, err := store.RestoreLatest(path); err != nil {
		t.Fatalf("RestoreLatest: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("restored content = %q, want %q", got, original)
	}
}

// Two mutations in a row leave two checkpoints; restore returns the most
// recent pre-state, not the oldest.
func TestWriteTool_CheckpointRestoresMostRecentState(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	for _, content := range []string{"v2", "v3"} {
		res, err := tool.Execute(context.Background(), json.RawMessage(
			`{"path":"f.txt","content":"`+content+`\n","overwrite":true}`,
		), nil)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.IsError {
			t.Fatalf("write failed: %s", res.Content)
		}
	}

	store := NewCheckpointStore(tmp)
	if _, err := store.RestoreLatest(path); err != nil {
		t.Fatalf("RestoreLatest: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2\n" {
		t.Fatalf("restored content = %q, want %q", got, "v2\n")
	}
}

// A mutation whose checkpoint cannot be taken must not happen. Blocking the
// store (.git/archie-checkpoints is a regular file) must leave the file
// untouched and the tool reporting an error, never a silent write.
func TestWriteTool_RefusesMutationWhenCheckpointFails(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "original\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	blockCheckpointStore(t, tmp)

	tool := NewWriteTool(tmp, NewMutationQueue(), nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"f.txt","content":"changed\n","overwrite":true}`,
	), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected the write to be refused without a checkpoint, got: %#v", res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("file was mutated without a checkpoint: %q", got)
	}
}

// Same fail-closed rule for edit.
func TestEditTool_RefusesMutationWhenCheckpointFails(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	original := "one\ntwo\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	blockCheckpointStore(t, tmp)

	tool := NewEditTool(tmp, NewMutationQueue(), nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"f.txt","edits":[{"old_text":"two","new_text":"TWO"}]}`,
	), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected the edit to be refused without a checkpoint, got: %#v", res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("file was mutated without a checkpoint: %q", got)
	}
}

// blockCheckpointStore makes the checkpoint store's parent a regular file so
// MkdirAll of the store directory cannot succeed.
func blockCheckpointStore(t *testing.T, workspace string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "archie-checkpoints"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
}
