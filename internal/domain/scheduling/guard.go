package scheduling

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrDangerousCommand is returned when a job's payload names a command nobody
// should schedule. The list is deliberately over-broad: refusing a job an
// operator wanted costs a retry, accepting one costs the host.
var ErrDangerousCommand = errors.New("scheduling: payload names a dangerous command")

// dangerousPayload matches the daemon's own restart, a service or launch
// manager, a process killer, a filesystem format, a raw block write and a
// machine power command. Matching is whole-word and case-insensitive, so "dd"
// is caught while "added" is not.
var dangerousPayload = regexp.MustCompile(`(?i)\b(?:archied restart|systemctl|launchctl|pkill|mkfs|dd|shutdown|reboot)\b`)

// RefuseDangerousJob refuses a job whose payload names a dangerous command.
// The refusal is at creation time because a scheduled job runs unattended:
// there is nobody at tick time to stop it. Every creation path reaches a job
// spec through this one function, so a new token is added here and nowhere
// else.
func RefuseDangerousJob(job JobSpec) error {
	if named := dangerousPayload.FindString(job.Payload.Text); named != "" {
		return fmt.Errorf("%w: job %q payload names %q", ErrDangerousCommand, job.ID, named)
	}
	return nil
}
