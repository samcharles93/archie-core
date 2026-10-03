package releaseupdate

import (
	"sort"
	"strings"
)

// Component IDs used by the install script and watchdog.
const (
	ComponentDaemon = "daemon"
	ComponentAgent  = "agent"
	// ComponentNATS identifies the NATS server.
	ComponentNATS = "nats"
)

// Placeholder versions that are never compared: VersionUnknown from the
// watchdog, VersionDev from unstamped builds.
const (
	VersionUnknown = "unknown"
	VersionDev     = "dev"
)

// VersionDrift is a component running a different version than the one
// claimed installed.
type VersionDrift struct {
	ID      string
	Claimed string
	Running string

	// RunningIsPrevious reports whether the running version is the one the
	// update replaced.
	RunningIsPrevious bool
}

// Verification sorts each claimed component into confirmed, drifted or
// unverified.
type Verification struct {
	// Confirmed lists component IDs whose claimed version matches what the
	// component reports for itself, in ID order.
	Confirmed []string
	// Drift lists claims the running system actively contradicts, in ID
	// order. Non-empty means the update did not take effect.
	Drift []VersionDrift
	// Unverified lists component IDs that could not be checked either way,
	// in ID order -- nothing recorded what was installed, or the component
	// does not report a real version for itself.
	Unverified []string
}

// Verify compares the versions r claims were installed with the versions
// components report running. Components absent from running are
// unverified.
func (r Report) Verify(running map[string]string) Verification {
	var result Verification
	for id, claimed := range r.Installed {
		claimedVersion, claimable := comparableVersion(claimed)
		actualVersion, actualKnown := comparableVersion(running[id])
		switch {
		case !claimable || !actualKnown:
			result.Unverified = append(result.Unverified, id)
		case claimedVersion == actualVersion:
			result.Confirmed = append(result.Confirmed, id)
		default:
			previousVersion, previousKnown := comparableVersion(r.Previous[id])
			result.Drift = append(result.Drift, VersionDrift{
				ID:                id,
				Claimed:           claimed,
				Running:           running[id],
				RunningIsPrevious: previousKnown && actualVersion == previousVersion,
			})
		}
	}
	sort.Strings(result.Confirmed)
	sort.Strings(result.Unverified)
	sort.Slice(result.Drift, func(i, j int) bool { return result.Drift[i].ID < result.Drift[j].ID })
	return result
}

// IsRecordedVersion reports whether version names an actual release rather
// than a placeholder standing in for one. Renderers use it to avoid printing
// a version change like "1.0.0 -> unknown", which reads as a downgrade to a
// release by that name.
func IsRecordedVersion(version string) bool {
	_, ok := comparableVersion(version)
	return ok
}

// comparableVersion trims whitespace and a leading "v", and reports whether
// the version is real.
func comparableVersion(version string) (string, bool) {
	normalized := strings.TrimSpace(version)
	if len(normalized) > 0 && (normalized[0] == 'v' || normalized[0] == 'V') {
		normalized = normalized[1:]
	}
	if normalized == "" ||
		strings.EqualFold(normalized, VersionUnknown) ||
		strings.EqualFold(normalized, VersionDev) {
		return "", false
	}
	return normalized, true
}
