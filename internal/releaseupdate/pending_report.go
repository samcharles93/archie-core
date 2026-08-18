package releaseupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Report is the phase-2 outcome of an update: whether the daemon came back
// up healthy after restarting, written by whichever process determined that
// (the deployment's restart/health-check tooling) and read, relayed to
// Channel/ChatID/ThreadID, and cleared by whichever archied process boots
// next -- which may be the newly installed version (success) or the
// previous one, if a crash-loop forced a rollback before this code could
// ever run on the new version.
type Report struct {
	Channel     string            `json:"channel"`
	ChatID      int64             `json:"chat_id"`
	ThreadID    int               `json:"thread_id"`
	Previous    map[string]string `json:"previous"`
	Installed   map[string]string `json:"installed"`
	HealthCheck string            `json:"health_check"` // "passed" or "failed"
	RolledBack  bool              `json:"rolled_back"`
}

func (r Report) validate() error {
	if r.RolledBack && r.HealthCheck != "failed" {
		return errors.New("update report: rolled_back requires health_check to be \"failed\"")
	}
	return nil
}

// WritePendingReport persists r to path atomically, the same way
// saveDeferrals does.
func WritePendingReport(path string, r Report) error {
	if err := r.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode update report: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return fmt.Errorf("write update report: %w", err)
	}
	return nil
}

// ReadPendingReport reads a Report previously written by WritePendingReport.
// found is false, with a nil error, when no update is in flight -- the
// common case on every normal startup.
func ReadPendingReport(path string) (Report, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, fmt.Errorf("read update report: %w", err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, false, fmt.Errorf("decode update report: %w", err)
	}
	return report, true, nil
}

// ClearPendingReport removes a report once it has been relayed. A report
// that is already gone (cleared by a previous boot attempt) is not an
// error.
func ClearPendingReport(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear update report: %w", err)
	}
	return nil
}
