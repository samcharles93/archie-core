// Package drain owns the decision logic for external drain requests.
//
// A drain is an operator-initiated request for the daemon to stop accepting
// new work and shut itself down gracefully. It arrives out-of-band through a
// marker file (an external trigger such as a maintenance window or a rolling
// deploy writes it) rather than a signal, because a marker survives the phase
// in which the process is between restarts and can be validated against the
// *current* instantiation.
//
// The single source of confusion this package rules out is the stale marker: a
// drain request written before the daemon last restarted must never drain the
// daemon that came up afterwards. That is handled by pairing each marker with
// an instantiation epoch (the OS boot id plus PID 1's start time) and only
// honouring a marker whose epoch matches the current one.
package drain

// Epoch identifies one instantiation of the daemon's process tree. It
// combines the OS boot id with PID 1's start time (in clock ticks since
// boot). Two Epochs are equal only when both parts match, so a marker written
// before a reboot or before a PID 1 restart within the same boot is never
// mistaken for one written against the current instantiation.
type Epoch struct {
	// BootID is the content of /proc/sys/kernel/random/boot_id, a UUID that
	// changes on every boot.
	BootID string `json:"boot_id"`
	// PID1Start is field 22 (starttime) of /proc/1/stat, in clock ticks since
	// boot. It distinguishes a container or PID 1 restart that happened
	// without a reboot.
	PID1Start int64 `json:"pid1_start"`
}

// Equal reports whether e and other identify the same instantiation.
func (e Epoch) Equal(other Epoch) bool {
	return e.BootID == other.BootID && e.PID1Start == other.PID1Start
}

// Empty reports whether the epoch carries no usable identity facts. An empty
// epoch cannot validate any marker, so callers must fail closed (never drain)
// rather than trust the marker.
func (e Epoch) Empty() bool {
	return e.BootID == "" || e.PID1Start <= 0
}

// Marker is the parsed .drain_request.json written by an external drain
// trigger. Only InstantiationEpoch is load-bearing for the decision; the
// optional operator fields (Reason, RequestedAt) are informational and do not
// influence whether the request is honoured.
type Marker struct {
	// InstantiationEpoch records the epoch of the daemon the request targets.
	InstantiationEpoch Epoch `json:"instantiation_epoch"`
	// Reason is an optional operator note (e.g. "maintenance window").
	Reason string `json:"reason,omitempty"`
	// RequestedAt is an optional RFC 3339 timestamp.
	RequestedAt string `json:"requested_at,omitempty"`
}

// Decision is the validation outcome for a marker against the current epoch.
type Decision int

const (
	// DecisionNone means no drain request is present (no marker, or the source
	// reported nothing). No drain.
	DecisionNone Decision = iota
	// DecisionStale means a marker is present but its epoch does not match the
	// current instantiation -- it was written before the last restart and must
	// be ignored. No drain.
	DecisionStale
	// DecisionValid means a marker is present and its epoch matches the current
	// instantiation: a live drain request that must trigger graceful shutdown.
	DecisionValid
)

func (d Decision) String() string {
	switch d {
	case DecisionNone:
		return "none"
	case DecisionStale:
		return "stale"
	case DecisionValid:
		return "valid"
	default:
		return "unknown"
	}
}

// Decide returns the drain decision for marker against current.
//
// A nil marker means no request (DecisionNone). An empty current epoch means
// the OS identity facts could not be established, so the request is treated as
// stale -- a daemon that cannot prove the marker is live must not shut itself
// down on it. Only a marker whose epoch exactly matches a non-empty current
// epoch is honoured.
func Decide(current Epoch, marker *Marker) Decision {
	if marker == nil {
		return DecisionNone
	}
	if current.Empty() {
		return DecisionStale
	}
	if !marker.InstantiationEpoch.Equal(current) {
		return DecisionStale
	}
	return DecisionValid
}
