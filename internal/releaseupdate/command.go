package releaseupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// CommandCatalog reads a Snapshot JSON document from an explicitly configured
// argv command. It never invokes a shell; deployment tooling remains an
// administrator-owned adapter.
type CommandCatalog struct{ Command []string }

func (c CommandCatalog) Check(ctx context.Context) (Snapshot, error) {
	if len(c.Command) == 0 {
		return Snapshot{}, fmt.Errorf("update check command is empty")
	}
	output, err := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...).Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("run update check: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode update check output: %w", err)
	}
	return snapshot, nil
}

type CommandInstaller struct{ Command []string }

func (i CommandInstaller) Install(ctx context.Context, progress func(string)) error {
	if len(i.Command) == 0 {
		return fmt.Errorf("update install command is empty")
	}
	progress("Installing approved update…")
	if output, err := exec.CommandContext(ctx, i.Command[0], i.Command[1:]...).CombinedOutput(); err != nil {
		return fmt.Errorf("run update installer: %w: %s", err, string(output))
	}
	return nil
}
