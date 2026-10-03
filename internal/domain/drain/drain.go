// Package drain decides whether a drain marker file applies to the running
// daemon. A marker is honoured only when its epoch matches the current one.
package drain

// Epoch identifies one boot of the process tree: the OS boot id and PID 1's
// start time.
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

// Decide returns the decision for marker. Nil means none; an empty current
// epoch or a mismatched marker is stale.
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
