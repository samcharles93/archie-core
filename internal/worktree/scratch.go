package worktree

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScratchDir is the workspace of a task that works on no repository. It sits
// beside the worktrees but outside their owner/repo tree, so no worktree path
// can name it.
func (m *Manager) ScratchDir(taskID int64) string {
	return filepath.Join(m.WorkDir, "scratch", fmt.Sprintf("task-%d", taskID))
}

// PrepareScratch creates an empty scratch workspace for a task, discarding
// whatever an earlier attempt left there.
func (m *Manager) PrepareScratch(taskID int64) (string, error) {
	dir := m.ScratchDir(taskID)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("reset scratch workspace: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create scratch workspace: %w", err)
	}
	return dir, nil
}

// RemoveScratch deletes a task's scratch workspace.
func (m *Manager) RemoveScratch(taskID int64) error {
	return os.RemoveAll(m.ScratchDir(taskID))
}
