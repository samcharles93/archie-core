// Lifted from tau internal/agent/tools/stderr_redact.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"bufio"
	"io"
	"log/slog"
	"regexp"
	"time"
)

// Redaction patterns per docs/specs/agents/03-wire-protocol.md (stderr
// handling: Redaction) and 02-spawning-and-lifecycle.md (Redaction
// contract). reBearerToken has no minimum length - the spec's own
// redaction test requirement uses a 10-character example token
// ("tok_abc123"), shorter than the {20,} it documents elsewhere for the
// sk- pattern, so this errs toward redacting too much rather than missing
// the documented test case.
var (
	reBearerToken = regexp.MustCompile(`Bearer\s+[a-zA-Z0-9_\-.]+`)
	reSkKey       = regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`)
	reEnvSecret   = regexp.MustCompile(`(?i)\b([A-Za-z_]*(?:KEY|TOKEN|SECRET|PASSWORD)[A-Za-z_]*)=\S+`)
)

// redactSecrets replaces known secret patterns in a single stderr line with
// [REDACTED]. Order matters only for overlap avoidance; re-matching an
// already-redacted "[REDACTED]" value is harmless (idempotent).
func redactSecrets(line string) string {
	line = reBearerToken.ReplaceAllString(line, "Bearer [REDACTED]")
	line = reSkKey.ReplaceAllString(line, "[REDACTED]")
	line = reEnvSecret.ReplaceAllString(line, "$1=[REDACTED]")
	return line
}

const (
	// stderrRateLimitBytesPerSec and stderrRateLimitWindow implement "4096
	// bytes per second, averaged over a 5-second window" - bursts within
	// the window are allowed (a full panic trace is captured), sustained
	// high-volume output past the window budget is truncated.
	stderrRateLimitBytesPerSec = 4096
	stderrRateLimitWindow      = 5 * time.Second
	// stderrMaxCaptureBytes is the total per-instance capture cap; excess
	// is discarded (the pipe is still drained so a chatty child never
	// blocks on a full stderr pipe buffer).
	stderrMaxCaptureBytes = 64 * 1024
)

// drainChildStderr reads r line-by-line, redacts known secret patterns,
// rate-limits and caps total capture, and logs surviving lines at ERROR
// level tagged with the child's instance ID. Never parsed for protocol
// messages - stderr is diagnostic-only (see docs/specs/agents/
// 03-wire-protocol.md). Blocks until r is exhausted (EOF on process exit);
// callers run it in its own goroutine.
func drainChildStderr(r io.Reader, instanceID string) {
	drainChildStderrWithClock(r, instanceID, time.Now)
}

// drainChildStderrWithClock is drainChildStderr with an injectable clock,
// so tests can advance the rate-limit window deterministically instead of
// depending on real wall-clock time passing between reads.
func drainChildStderrWithClock(r io.Reader, instanceID string, now func() time.Time) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024) // tolerate long lines without unbounded growth

	windowBudget := stderrRateLimitBytesPerSec * int(stderrRateLimitWindow/time.Second)
	var (
		totalCaptured         int
		capReported           bool
		windowStart           = now()
		windowBytes           int
		rateLimitedThisWindow bool
	)

	for scanner.Scan() {
		line := scanner.Text()
		lineBytes := len(line) + 1 // + newline, matching the byte-budget the spec describes

		if totalCaptured >= stderrMaxCaptureBytes {
			if !capReported {
				slog.Error("[... stderr truncated: capture limit exceeded ...]", "instance_id", instanceID)
				capReported = true
			}
			continue
		}

		if t := now(); t.Sub(windowStart) >= stderrRateLimitWindow {
			windowStart = t
			windowBytes = 0
			rateLimitedThisWindow = false
		}
		windowBytes += lineBytes
		if windowBytes > windowBudget {
			if !rateLimitedThisWindow {
				slog.Error("[... stderr truncated: rate limit exceeded ...]", "instance_id", instanceID)
				rateLimitedThisWindow = true
			}
			continue
		}

		totalCaptured += lineBytes
		slog.Error(redactSecrets(line), "instance_id", instanceID)
	}
}
