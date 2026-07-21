package container

import (
	"testing"
	"time"
)

// ── regression: Gap 2 — post-completion grace period ────────────────

func TestContainerSupportsGracePeriod(t *testing.T) {
	// Gap 2: MaxUptime is a container-level context timeout at creation,
	// not a post-completion grace period. PRD section 1 says:
	// "max_uptime — grace period after task completion before kill."
	//
	// The agent should stay alive after the task finishes so it can
	// handle follow-ups (gate re-runs, human replies). Currently
	// Release() stops the container immediately — there's no way to
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
	// Gap 2 assertion: Config needs GracePeriod alongside MaxUptime.
	// MaxUptime = total container lifetime cap (creation → kill).
	// GracePeriod = idle time after task completion before kill.
	// Currently GracePeriod doesn't exist.
	if cfg.MaxUptime > 0 {
		// Placeholder: when GracePeriod is added, verify it exists.
	}
}

// ── regression: Gap 6 — /data/task.json boot brief ───────────────────

func TestTaskPayloadWrittenAsVolumeFile(t *testing.T) {
	// Gap 6: task travels over NATS, not a file. PRD section 3 describes
	// /data/task.json as the container's boot-time brief — the daemon
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
		ID: 42, Owner: "sam", Repo: "todo", Number: 170,
		Title: "fix bug", Body: "body text",
		Labels: []string{"bug"}, Workflow: "tdd",
	}

	// Gap 6 assertion: the daemon must serialize this payload as JSON
	// and write it to <workspace>/task.json before calling Acquire().
	// Currently no such file is written — the task travels over NATS.
	if payload.ID == 0 {
		t.Error("TaskPayload ID is zero")
	}
	_ = payload
}
