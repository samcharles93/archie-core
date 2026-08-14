package agentboot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/container"
)

func bootGitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTaskIDDistinguishesSharedDedicatedAndInvalidBoots(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		write   bool
		wantID  int64
		wantOK  bool
		wantErr bool
	}{
		{name: "absent uses shared mode"},
		{name: "valid uses dedicated mode", payload: mustTaskPayload(t, container.TaskPayload{ID: 42}), write: true, wantID: 42, wantOK: true},
		{name: "malformed fails closed", payload: []byte("not json"), write: true, wantErr: true},
		{name: "zero ID fails closed", payload: mustTaskPayload(t, container.TaskPayload{}), write: true, wantErr: true},
		{name: "negative ID fails closed", payload: mustTaskPayload(t, container.TaskPayload{ID: -1}), write: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := bootGitDir(t)
			if test.write {
				if err := os.WriteFile(filepath.Join(dir, ".git", "task.json"), test.payload, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			id, ok, err := TaskID(dir)
			if (err != nil) != test.wantErr {
				t.Fatalf("TaskID() error = %v, wantErr %v", err, test.wantErr)
			}
			if id != test.wantID || ok != test.wantOK {
				t.Fatalf("TaskID() = (%d, %v), want (%d, %v)", id, ok, test.wantID, test.wantOK)
			}
		})
	}
}

func TestTaskIDFailsClosedOnNonMissingReadError(t *testing.T) {
	dir := bootGitDir(t)
	path := filepath.Join(dir, ".git", "task.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	id, dedicated, err := TaskID(dir)
	if err == nil {
		t.Fatal("TaskID() error = nil for unreadable task.json path")
	}
	if id != 0 || dedicated {
		t.Fatalf("TaskID() = (%d, %v), want (0, false)", id, dedicated)
	}
}

func mustTaskPayload(t *testing.T, payload container.TaskPayload) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
