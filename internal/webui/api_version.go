package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// componentStatus is the per-component update status enum surfaced to the
// dashboard: an operator scanning this list needs "is this component fine,
// or does it need attention, and why" at a glance, not a bare color that
// means something different on every row.
const (
	// statusOK means the running version matches what the check command
	// reports installed, and nothing newer is available.
	statusOK = "ok"
	// statusUpdateAvailable means a newer version exists and nothing
	// contradicts the check command's claim of what's currently running.
	statusUpdateAvailable = "update_available"
	// statusDrift means a running version was actually observed and it
	// does not match what the check command claims is installed -- the
	// same false-success shape releaseupdate.Report.Verify catches after
	// an update, but here for the steady-state view.
	statusDrift = "drift"
	// statusUnknown means no running version could be confirmed for this
	// component, so neither ok nor drift can be asserted honestly.
	statusUnknown = "unknown"
)

// componentVersionStatus is one row of GET /api/version: what a component
// claims to have installed, what was actually observed running (if
// anything), what's available, how it's deployed, and the resulting status.
type componentVersionStatus struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	InstallType     string `json:"install_type,omitempty"`
	Reference       string `json:"reference,omitempty"`
	RunningVersion  string `json:"running_version,omitempty"`
	InstalledClaim  string `json:"installed_claim,omitempty"`
	LatestAvailable string `json:"latest_available,omitempty"`
	Status          string `json:"status"`
}

// versionStatusFor decides one component's status from its check-command
// claim (installed) against the version actually observed running (running,
// ok). Order matters: drift is checked before update_available because a
// claim the running system actively contradicts is a more fundamental
// problem than a newer release merely existing -- an operator who only
// sees "update available" on a component that is silently not running what
// it claims would look in the wrong place.
func versionStatusFor(component releaseupdate.Component, running string, ok bool) string {
	if ok && component.Installed != "" && running != component.Installed {
		return statusDrift
	}
	if !ok || component.Installed == "" {
		// No baseline to compare against -- an empty Installed means the
		// check command never reported one, so Available (if any) cannot
		// be judged newer than nothing. Reporting update_available here
		// would assert a comparison that was never actually made.
		return statusUnknown
	}
	if component.Available != "" && component.Available != component.Installed {
		return statusUpdateAvailable
	}
	return statusOK
}

// handleVersion reports, per component, how it's deployed and whether what
// is actually running matches what the operator's update-check command
// claims -- the steady-state counterpart to the phase-2 update report
// (update_report.go), which only fires around an update itself.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if s.Chat == nil || !chatUpdateServiceConfigured(s.Chat.Updates) {
		http.Error(w, "updates are not configured", http.StatusNotImplemented)
		return
	}
	snapshot, err := s.Chat.Updates.Check(r.Context(), 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	running := map[string]string{}
	if s.RunningVersions != nil {
		running = s.RunningVersions()
	}
	components := make([]componentVersionStatus, 0, len(snapshot.Components))
	for _, component := range snapshot.Components {
		runningVersion, ok := running[component.ID]
		if ok && runningVersion == "" {
			ok = false
		}
		components = append(components, componentVersionStatus{
			ID: component.ID, Label: component.Label,
			InstallType: component.InstallType, Reference: component.Reference,
			RunningVersion: runningVersion, InstalledClaim: component.Installed,
			LatestAvailable: component.Available,
			Status:          versionStatusFor(component, runningVersion, ok),
		})
	}
	writeJSON(w, map[string]any{"components": components})
}
