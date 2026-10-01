package workflow

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// gateEnvironmentCause reports why a failed gate command is an environment
// failure the baseline repair agent cannot fix, or "" when the failure is an
// ordinary code failure the agent can act on.
//
// Only two signals are treated as reliable enough to act on:
//
//   - the command could not be started at all. os/exec reports a missing or
//     non-executable binary as *exec.Error before the gate runs a line of its
//     own, and no source change in the repository can make a tool that is not
//     installed appear.
//   - the combined output names host infrastructure the gate needs and does
//     not have, such as a Docker daemon the test harness spawns containers on.
//     These are the phrasings the tools themselves emit when their dependency
//     is absent (archie-core-71mq), not an inference drawn from the exit
//     status.
//
// The second signal is deliberately a closed, literal set: a marker is added
// with the incident that produced it, never by inference. A false negative
// only spends the repair budget a code failure would have spent anyway, but a
// false positive would park a task the builder could have fixed.
func gateEnvironmentCause(out string, runErr error) string {
	if execErr, ok := errors.AsType[*exec.Error](runErr); ok {
		return "the gate command could not be started (" + execErr.Error() + ")"
	}
	lower := strings.ToLower(out)
	for _, m := range gateEnvironmentMarkers {
		if strings.Contains(lower, m.marker) {
			return m.cause
		}
	}
	return ""
}

// gateEnvironmentMarkers pairs an output fragment a missing host resource
// produces with the cause it names. Markers are matched case-insensitively
// against the gate's combined output.
var gateEnvironmentMarkers = []struct {
	marker string
	cause  string
}{
	{
		marker: "is docker running?",
		cause:  "the Docker daemon required by the test harness is unavailable",
	},
	{
		marker: "cannot connect to the docker daemon",
		cause:  "the Docker daemon is unavailable",
	},
	{
		marker: "rootless docker not found",
		cause:  "the Docker daemon is unavailable",
	},
	{
		marker: "failed to create docker provider",
		cause:  "the Docker daemon is unavailable",
	},
	{
		marker: "docker: command not found",
		cause:  "the docker CLI is not installed",
	},
}

// baselineEnvironmentError is the park reason when a gate failed for an
// environment cause: it names the cause and keeps the gate output an operator
// needs to confirm it. The stage returns this without dispatching a builder.
func baselineEnvironmentError(argv []string, out, cause string) error {
	return fmt.Errorf("baseline red  --  %s cannot run in this environment: %s\n\ngate output:\n%s",
		strings.Join(argv, " "), cause, clipTail(out, baselineParkOutputBytes))
}

// baselineFixFailedError is the park reason for every failure after the
// baseline repair agent was dispatched. A failure that names only the agent --
// a provider 402, say -- and drops the gate output leaves the operator with no
// way to see why the gate was red in the first place (archie-core-rwy6).
func baselineFixFailedError(argv []string, out, cause string) error {
	return fmt.Errorf("baseline red  --  %s fails and the baseline-fix builder did not repair it: %s\n\ngate output:\n%s",
		strings.Join(argv, " "), cause, clipTail(out, baselineParkOutputBytes))
}
