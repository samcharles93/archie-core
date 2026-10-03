package releaseupdate

// InstallMeta identifies who to notify after the update restarts.
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

// Result is the outcome of an install before restart. Previous and Installed
// map component IDs to versions.
type Result struct {
	Previous         map[string]string `json:"previous"`
	Installed        map[string]string `json:"installed"`
	Components       []ComponentResult `json:"components"`
	RestartRequested bool              `json:"restart_requested"`
}
