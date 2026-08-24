package agentboot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/container"
)

// TaskID reads the daemon-owned boot brief at <mountDir>/.git/task.json.
// The brief is mandatory: every worker belongs to exactly one daemon-created
// task container, so missing, unreadable, malformed, or invalid metadata is a
// startup error rather than permission to consume work for another task.
func TaskID(mountDir string) (int64, error) {
	path := filepath.Join(mountDir, ".git", "task.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read mandatory task boot metadata %s: %w", path, err)
	}

	var payload container.TaskPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, fmt.Errorf("decode %s: %w", path, err)
	}
	if payload.ID <= 0 {
		return 0, fmt.Errorf("decode %s: task ID must be positive, got %d", path, payload.ID)
	}
	return payload.ID, nil
}
