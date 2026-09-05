// Package drain is the infrastructure implementation of drain-request
// detection. It reads the external marker file from disk and the OS identity
// facts that make up the current instantiation epoch, then hands the verdict
// to the domain's Decide function.
//
// It only reads. Deciding (and any shutdown consequence) belongs to the caller,
// so the daemon never drains itself from this package.
package drain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/drain"
)

// DefaultMarkerFilename is the marker file the daemon watches for an external
// drain request.
var DefaultMarkerFilename = ".drain_request.json"

// CurrentEpoch reads the OS identity facts that identify the current
// instantiation: the boot id (a UUID that changes every boot) and PID 1's
// start time (in clock ticks since boot), which distinguishes a container or
// PID 1 restart that happened without a reboot.
func CurrentEpoch() (drain.Epoch, error) {
	bootID, err := readBootID()
	if err != nil {
		return drain.Epoch{}, fmt.Errorf("read boot id: %w", err)
	}
	pid1Start, err := readPid1Start()
	if err != nil {
		return drain.Epoch{}, fmt.Errorf("read pid 1 start time: %w", err)
	}
	return drain.Epoch{BootID: bootID, PID1Start: pid1Start}, nil
}

// readBootID returns the kernel boot id from
// /proc/sys/kernel/random/boot_id. It returns an error when the file cannot be
// read, so an unverifiable epoch fails closed in the caller.
func readBootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	bootID := strings.TrimSpace(string(data))
	if bootID == "" {
		return "", errors.New("empty boot id")
	}
	return bootID, nil
}

// readPid1Start returns PID 1's start time from /proc/1/stat.
func readPid1Start() (int64, error) {
	data, err := os.ReadFile("/proc/1/stat")
	if err != nil {
		return 0, err
	}
	return parseProcStat(string(data))
}

// parseProcStat extracts PID 1's start time (field 22, starttime) from the
// textual /proc/[pid]/stat record. The comm field (field 2) is wrapped in
// parentheses and may itself contain spaces or parentheses, so the record is
// split after the last closing parenthesis and field 22 is the 20th token
// there (post-paren token 0 is field 3, the state).
func parseProcStat(stat string) (int64, error) {
	paren := strings.LastIndexByte(stat, ')')
	if paren < 0 {
		return 0, errors.New("malformed proc stat: no closing parenthesis")
	}
	fields := strings.Fields(stat[paren+1:])
	// After the parenthesis the tokens are fields 3..N, so field 22 is
	// index 22-3 = 19.
	if len(fields) < 20 {
		return 0, fmt.Errorf("malformed proc stat: %d post-paren fields, want >= 20", len(fields))
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse pid 1 start time %q: %w", fields[19], err)
	}
	if start <= 0 {
		return 0, fmt.Errorf("invalid pid 1 start time %d", start)
	}
	return start, nil
}

// Reader reads a drain marker file and the current instantiation epoch,
// reporting whether a live drain request exists right now. The epoch source is
// injectable so tests can exercise the full read-and-decide path without
// touching /proc.
type Reader struct {
	path  string
	epoch func() (drain.Epoch, error)
}

// New builds a Reader for the marker file at markerPath. When epoch is nil the
// package's CurrentEpoch is used; pass a fake epoch function in tests to pin
// the current instantiation without depending on the host.
func New(markerPath string, epoch func() (drain.Epoch, error)) *Reader {
	if epoch == nil {
		epoch = CurrentEpoch
	}
	return &Reader{path: markerPath, epoch: epoch}
}

// Check returns the drain decision for the marker at the reader's path against
// the current epoch.
//
// A missing marker returns DecisionNone. The returned error is informational --
// it exists so a caller can log why a request was not honoured -- but the
// decision is authoritative and always fails closed on an unrecoverable read
// (permission error, malformed JSON, unreadable epoch) so a broken marker can
// never drain the daemon.
func (r *Reader) Check() (drain.Decision, error) {
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return drain.DecisionNone, nil
	}
	if err != nil {
		return drain.DecisionStale, fmt.Errorf("read drain marker: %w", err)
	}
	var marker drain.Marker
	if err := json.Unmarshal(data, &marker); err != nil {
		return drain.DecisionStale, fmt.Errorf("decode drain marker: %w", err)
	}
	current, err := r.epoch()
	if err != nil {
		return drain.DecisionStale, fmt.Errorf("resolve instantiation epoch: %w", err)
	}
	return drain.Decide(current, &marker), nil
}
