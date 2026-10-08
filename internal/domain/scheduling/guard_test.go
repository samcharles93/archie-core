package scheduling

import (
	"errors"
	"strings"
	"testing"
)

func TestRefuseDangerousJob(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "an ordinary body", payload: "Summarise yesterday's commits and open a PR."},
		{name: "letters inside a word", payload: "Add a note about the middleware address."},
		{name: "a gerund", payload: "Report on the rebooting schedule."},
		{name: "the daemon's own restart", payload: "archied restart", want: "archied restart"},
		{name: "a service manager", payload: "sudo systemctl restart archied", want: "systemctl"},
		{name: "a launch manager", payload: "launchctl unload ~/Library/LaunchAgents/x.plist", want: "launchctl"},
		{name: "a process killer", payload: "pkill -f archied", want: "pkill"},
		{name: "a filesystem format", payload: "mkfs.ext4 /dev/sda1", want: "mkfs"},
		{name: "a raw block write", payload: "sudo dd if=/dev/zero of=/dev/sda", want: "dd"},
		{name: "a machine power command", payload: "shutdown -h now", want: "shutdown"},
		{name: "a restart in capitals", payload: "Please REBOOT the box", want: "REBOOT"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			job := JobSpec{ID: "nightly", Payload: Payload{Text: testCase.payload}}
			err := RefuseDangerousJob(job)
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("RefuseDangerousJob(%q) = %v, want nil", testCase.payload, err)
				}
				return
			}
			if !errors.Is(err, ErrDangerousCommand) {
				t.Fatalf("RefuseDangerousJob(%q) = %v, want ErrDangerousCommand", testCase.payload, err)
			}
			for _, want := range []string{testCase.want, `"nightly"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("RefuseDangerousJob(%q) = %q, want it to name %s", testCase.payload, err, want)
				}
			}
		})
	}
}
