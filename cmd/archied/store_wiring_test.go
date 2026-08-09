package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionTaskStoreUsesSQLite(t *testing.T) {
	configuredPath := filepath.Join(t.TempDir(), "state.db")
	path := taskDBPath(configuredPath)
	if path == configuredPath {
		t.Fatal("task database must use an independent fresh path")
	}
	st, err := openProductionTaskStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configuredPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configured anchor path was opened directly: %v", err)
	}
	inserted, err := st.EnqueueIssue(t.Context(), "acme", "widget", 7, "persisted", "", "", "")
	if err != nil || !inserted {
		t.Fatalf("EnqueueIssue = (%v, %v), want (true, nil)", inserted, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = openProductionTaskStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	task, err := st.TaskByIssue(t.Context(), "acme", "widget", 7)
	if err != nil || task == nil || task.Title != "persisted" {
		t.Fatalf("TaskByIssue after reopen = (%+v, %v)", task, err)
	}
}
