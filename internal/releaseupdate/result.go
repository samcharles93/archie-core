package releaseupdate

// InstallMeta identifies who to notify once an installed update finishes
// restarting. The process that calls Install returns before the daemon
// actually restarts (the reference installer queues the restart through a
// detached unit so restarting archied.service doesn't kill the installer
// mid-run), so this identity has to survive the restart on disk -- see
// Report and WritePendingReport.
type InstallMeta struct {
	Channel  string
	ChatID   int64
	ThreadID int
	// ReportPath is where the installer's restart/health-check tooling
	// should write the phase-2 Report once it knows the outcome -- see
	// WritePendingReport. Empty means the deployment has no phase-2
	// reporting configured (the caller only gets the synchronous Result).
	ReportPath string
}

// ComponentResult reports what an installer actually did to one component.
type ComponentResult struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // "updated", "unchanged", or "failed"
}

// Result is the structured outcome of the synchronous phase of an install:
// fetch/build/install, before any restart happens. Previous and Installed
// map a component ID to its version. Field names are snake_case in JSON to
// match the install script's ARCHIE_UPDATE_RESULT sentinel line -- see
// command.go and scripts/archie-update-install.
type Result struct {
	Previous         map[string]string `json:"previous"`
	Installed        map[string]string `json:"installed"`
	Components       []ComponentResult `json:"components"`
	RestartRequested bool              `json:"restart_requested"`
}
