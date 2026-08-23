package releaseupdate

import (
	"sort"
	"strings"
)

// Component IDs used by the reference install script and watchdog in the
// Result and Report JSON documents (scripts/archie-update-install,
// scripts/archie-update-watchdog). They are named here because the
// composition root has to key its running-version map by exactly these
// strings for verification to match anything -- a typo would otherwise
// silently downgrade every component to "unverifiable" and restore the
// false-success behaviour this file exists to prevent.
const (
	ComponentDaemon = "daemon"
	ComponentAgent  = "agent"
	// ComponentNATS identifies the NATS server in a Snapshot's Components,
	// by the same convention -- a third-party dependency archie doesn't
	// build and Report never claims to have installed, but Service.Enrich
	// can still describe from deployment configuration (embedded vs
	// external, and the URL an external server was configured with).
	ComponentNATS = "nats"
)

// Version placeholders that carry no release information. Both mean "nobody
// recorded a real version here", and neither may be compared as though it
// were one: doing so turns a healthy update whose bookkeeping went missing
// into a reported failure.
//
//   - VersionUnknown is what the reference watchdog substitutes when it
//     cannot tell what was installed ("${ARCHIE_UPDATE_INSTALLED_GATEWAY:-unknown}").
//   - VersionDev is what an unstamped build reports for itself: it is the
//     default of gatewayVersion/runtimeVersion in cmd/archied, of
//     GATEWAY_VERSION in Dockerfile.archied, and of the version Taskfile.yml
//     falls back to when git describes no tag. A plain `go build` or an
//     untagged image is therefore "dev", not a release.
const (
	VersionUnknown = "unknown"
	VersionDev     = "dev"
)

// VersionDrift is one component whose reported install the running system
// contradicts: the installer said it put Claimed into service, but the
// component itself reports it is running Running. Both are the versions as
// recorded rather than normalized, so an operator sees the strings the two
// sides actually wrote; renderers should trim them for display.
type VersionDrift struct {
	ID      string
	Claimed string
	Running string

	// RunningIsPrevious reports whether the version actually running is the
	// one this update set out to replace. True is the ordinary case -- the
	// install silently did nothing and the pre-update process is still there
	// answering the health probe. False means what is running is neither the
	// installed version nor the replaced one, and a caller must NOT describe
	// that as "still on the old release": it does not know what happened,
	// and asserting a cause it cannot support is the same defect as the
	// false success this type exists to catch.
	RunningIsPrevious bool
}

// Verification is the outcome of checking a Report's claims against the
// versions actually running. Every component the Report claims to have
// installed lands in exactly one of the three sets.
//
// Unverified is not a lesser Drift: a caller that cannot tell the two apart
// will either cry wolf over components that simply cannot self-report, or
// keep asserting success it never checked. Both have shipped here.
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

// Verify compares what r claims was installed against the versions actually
// running now.
//
// This exists because a Report is entirely self-reported. The reference
// watchdog decides "passed" from an HTTP health probe, which proves only
// that *a* daemon is answering -- if the new binary never actually made it
// into service, the previous one answers the probe just as happily, and the
// watchdog then copies the version the installer *intended* into Installed.
// The result reads as a successful upgrade to a version that is not running.
// That is not hypothetical: it shipped, and a task run immediately after a
// reported-successful update hit the exact bug the update was supposed to
// fix.
//
// running maps a component ID (see ComponentDaemon and ComponentAgent) to
// the version that component reports for itself -- archied's own compiled-in
// build version, for instance, which no installer can talk it out of. A
// component absent from running is Unverified, not drifting.
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

// comparableVersion reduces a recorded version to the form both sides of a
// comparison can be expected to agree on, and reports whether it carries any
// usable information at all.
//
// Normalization is deliberately minimal: surrounding whitespace, and a
// single leading "v" or "V" so an installer that records the git tag
// "v1.9.11" matches a binary stamped "1.9.11" (the reference installer
// strips that prefix itself, but nothing forces another deployment shape
// to). Everything else is compared exactly -- an installer is expected to
// record the version it stamped, not a differently formatted description of
// it, and guessing at looser equivalences here would start hiding the very
// mismatches this check is for.
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
