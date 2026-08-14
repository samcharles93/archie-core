package agentboot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/container"
)

// TaskID reads the daemon-owned boot brief at <mountDir>/.git/task.json.
// An absent brief selects shared-worker mode. Any other read error, malformed
// JSON, or non-positive task ID is a startup error: a container with invalid
// dedicated-task metadata must never join the shared queue.
func TaskID(mountDir string) (taskID int64, dedicated bool, err error) {
	path := filepath.Join(mountDir, ".git", "task.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", path, err)
	}

	var payload container.TaskPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, false, fmt.Errorf("decode %s: %w", path, err)
	}
	if payload.ID <= 0 {
		return 0, false, fmt.Errorf("decode %s: task ID must be positive, got %d", path, payload.ID)
	}
	return payload.ID, true, nil
}
