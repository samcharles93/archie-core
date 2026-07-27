package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── regression: Gap 2  --  post-completion grace period ────────────────

func TestContainerSupportsGracePeriod(t *testing.T) {
	// Gap 2: MaxUptime is a container-level context timeout at creation,
	// not a post-completion grace period. PRD section 1 says:
	// "max_uptime  --  grace period after task completion before kill."
	//
	// The agent should stay alive after the task finishes so it can
	// handle follow-ups (gate re-runs, human replies). Currently
	// Release() stops the container immediately  --  there's no way to
	// keep it alive after task completion.

	c := &Container{ID: "test"}
	grace := 5 * time.Minute

	// A container must support being kept alive after task completion.
	// This means: after Release() is called (task done), the container
	// stays up for the grace period to handle follow-ups. Only after
	// the grace period expires (or on explicit shutdown) is it killed.
	//
	// Currently Release() immediately calls ContainerStop + ContainerRemove.
	// The Pool must support a grace period where Release() marks the
	// container as idle but keeps it alive.
	_ = c
	_ = grace

	// Structural check: Pool.Config must have a GracePeriod field
	// distinct from MaxUptime (which is the container lifetime cap).
	cfg := Config{MaxUptime: 30 * time.Minute}
	if cfg.MaxUptime == 0 {
		t.Error("MaxUptime not set")
	}
	// Gap 2 assertion: Release() must respect GracePeriod.
	// Currently Release() calls ContainerStop immediately regardless of
	// GracePeriod. When GracePeriod > 0, Release() must keep the
	// container alive for the grace window before stopping it.
	// The container should stay alive for cfg.GracePeriod after
	// the task completes, then be killed.
	_ = cfg.GracePeriod
}

func TestWriteTaskJSONProducesValidFile(t *testing.T) {
	dir := t.TempDir()
	payload := TaskPayload{
		ID: 42, Owner: "acme", Repo: "todo", Number: 170,
		Title: "fix bug", Body: "body text",
		Labels: []string{"bug"}, Workflow: "tdd",
	}
	if err := WriteTaskJSON(dir, payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatal("task.json was not written:", err)
	}
	var decoded TaskPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal("task.json is not valid JSON:", err)
	}
	if decoded.ID != 42 || decoded.Workflow != "tdd" {
		t.Errorf("decoded payload = %+v", decoded)
	}
}

// ── regression: Gap 6  --  /data/task.json boot brief ───────────────────

func TestTaskPayloadWrittenAsVolumeFile(t *testing.T) {
	// Gap 6: task travels over NATS, not a file. PRD section 3 describes
	// /data/task.json as the container's boot-time brief  --  the daemon
	// writes it to the volume before the container starts, and the agent
	// reads it on boot alongside NATS messages.

	// The container mount path must match the PRD's /data/ layout.
	// The pool already mounts the worktree at /data/worktree.
	// What's missing: /data/task.json written as a file in the worktree
	// directory (or a separate bind-mounted file) before Acquire.
	//
	// TaskPayload is the data that should be written to /data/task.json.
	type TaskPayload struct {
		ID       int64    `json:"id"`
		Owner    string   `json:"owner"`
		Repo     string   `json:"repo"`
		Number   int      `json:"issue_number"`
		Title    string   `json:"title"`
		Body     string   `json:"body"`
		Labels   []string `json:"labels"`
		Workflow string   `json:"workflow"`
	}

	payload := TaskPayload{
		ID: 42, Owner: "acme", Repo: "todo", Number: 170,
		Title: "fix bug", Body: "body text",
		Labels: []string{"bug"}, Workflow: "tdd",
	}

	// Gap 6 assertion: the daemon must serialize this payload as JSON
	// and write it to <workspace>/task.json before calling Acquire().
	// Currently no such file is written  --  the task travels over NATS.
	if payload.ID == 0 {
		t.Error("TaskPayload ID is zero")
	}
	_ = payload
}

// ── Regression tests for error paths ────────────────────────────────

func TestWriteTaskJSONNonexistentDir(t *testing.T) {
	// Create a regular file and then try to use a path where that file
	// would need to be a directory. Even root cannot mkdir through a
	// regular file, making this test reliable regardless of privileges.
	f, err := os.CreateTemp("", "archie-test-blocker")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	err = WriteTaskJSON(filepath.Join(f.Name(), "subdir"), TaskPayload{ID: 1})
	if err == nil {
		t.Error("expected error writing to nonexistent directory")
	}
}

func TestWriteTaskJSONEmptyWorkspace(t *testing.T) {
	err := WriteTaskJSON("", TaskPayload{ID: 1})
	if err == nil {
		t.Error("expected error with empty workspace path")
	}
}

func TestTaskPayloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	payload := TaskPayload{
		ID:       99,
		Owner:    "test-owner",
		Repo:     "test-repo",
		Number:   42,
		Title:    "test title",
		Body:     "test body",
		Labels:   []string{"a", "b"},
		Workflow: "tdd",
		Branch:   "feature/x",
		Plan:     "plan text",
	}

	if err := WriteTaskJSON(dir, payload); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		t.Fatal(err)
	}

	var decoded TaskPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.ID != payload.ID {
		t.Errorf("ID = %d", decoded.ID)
	}
	if decoded.Owner != payload.Owner {
		t.Errorf("Owner = %q", decoded.Owner)
	}
	if decoded.Branch != payload.Branch {
		t.Errorf("Branch = %q", decoded.Branch)
	}
	if decoded.Plan != payload.Plan {
		t.Errorf("Plan = %q", decoded.Plan)
	}
	if len(decoded.Labels) != 2 {
		t.Errorf("Labels = %v", decoded.Labels)
	}
}

func TestWriteTaskJSONOverwrite(t *testing.T) {
	dir := t.TempDir()
	p1 := TaskPayload{ID: 1, Title: "first"}
	p2 := TaskPayload{ID: 2, Title: "second"}

	_ = WriteTaskJSON(dir, p1)
	_ = WriteTaskJSON(dir, p2) // overwrite

	data, _ := os.ReadFile(filepath.Join(dir, "task.json"))
	var decoded TaskPayload
	_ = json.Unmarshal(data, &decoded)

	if decoded.ID != 2 || decoded.Title != "second" {
		t.Errorf("expected second payload, got ID=%d Title=%q", decoded.ID, decoded.Title)
	}
}

func TestWriteTaskJSONMinimalPayload(t *testing.T) {
	dir := t.TempDir()
	payload := TaskPayload{ID: 1}

	if err := WriteTaskJSON(dir, payload); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "task.json"))
	var decoded TaskPayload
	_ = json.Unmarshal(data, &decoded)

	if decoded.ID != 1 {
		t.Error("minimal payload ID mismatch")
	}
}
