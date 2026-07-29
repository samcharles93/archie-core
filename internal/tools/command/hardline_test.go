package command_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools/command"
)

// TestHardlineBlocks covers the commands that must never run.
func TestHardlineBlocks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
		rule string
	}{
		{"root delete", "rm -rf /", "recursive-root-delete"},
		{"root delete glob", "rm -rf /*", "recursive-root-delete"},
		{"root delete dot", "rm -rf /.", "recursive-root-delete"},
		{"root delete long flag", "rm --recursive --force /", "recursive-root-delete"},
		{"root delete bundled reverse", "rm -fr /", "recursive-root-delete"},
		{"home delete", "rm -rf ~", "recursive-root-delete"},
		{"home var delete", "rm -rf $HOME", "recursive-root-delete"},
		{"home brace delete", "rm -rf ${HOME}/", "recursive-root-delete"},
		{"etc delete", "rm -rf /etc", "recursive-root-delete"},
		{"usr glob delete", "rm -rf /usr/*", "recursive-root-delete"},
		{"quoted root delete", `rm -rf "/"`, "recursive-root-delete"},
		{"env prefixed delete", "FOO=bar rm -rf /", "recursive-root-delete"},
		{"sudo delete", "sudo rm -rf /", "recursive-root-delete"},

		{"mkfs", "mkfs.ext4 /dev/sda1", "filesystem-format"},
		{"bare mkfs", "mkfs /dev/sdb", "filesystem-format"},

		{"dd to disk", "dd if=/dev/zero of=/dev/sda bs=1M", "block-device-write"},
		{"dd to nvme", "dd if=x.img of=/dev/nvme0n1", "block-device-write"},
		{"redirect to disk", "echo x > /dev/sda", "block-device-write"},

		{"fork bomb", ":(){ :|:& };:", "fork-bomb"},
		{"fork bomb compact", ":(){:|:&};:", "fork-bomb"},

		{"kill all", "kill -9 -1", "kill-all-processes"},

		{"shutdown", "shutdown -h now", "system-power"},
		{"reboot", "reboot", "system-power"},
		{"poweroff path", "/sbin/poweroff", "system-power"},
		{"init 0", "init 0", "system-power"},
		{"telinit 6", "telinit 6", "system-power"},
		{"systemctl reboot", "systemctl reboot", "system-power"},

		{"sudo stdin", "sudo -S ls", "sudo-stdin-password"},

		{"pkill daemon", "pkill archied", "daemon-self-termination"},
		{"killall daemon", "killall -9 archied", "daemon-self-termination"},
		{"systemctl stop daemon", "systemctl stop archied", "daemon-self-termination"},
		{"docker stop daemon", "docker stop archie-core-archied-1", "daemon-self-termination"},
		{"docker rm daemon", "docker rm -f archie-core-archied-1", "daemon-self-termination"},

		{"launch daemon", "archied -config /etc/archie/config.toml", "daemon-launch"},
		{"nohup daemon", "nohup archied &", "daemon-launch"},

		{"hidden after separator", "echo hello; rm -rf /", "recursive-root-delete"},
		{"hidden after and", "cd /tmp && rm -rf /", "recursive-root-delete"},
		{"hidden after pipe", "cat x | rm -rf /", "recursive-root-delete"},
		{"hidden in substitution", "echo $(rm -rf /)", "recursive-root-delete"},
		{"hidden in backticks", "echo `rm -rf /`", "recursive-root-delete"},
		{"hidden in subshell", "(rm -rf /)", "recursive-root-delete"},
		{"hidden after newline", "echo hi\nrm -rf /", "recursive-root-delete"},

		// Wrapped forms. These are not exotic: `bash -c` is a shape
		// models produce unprompted, and without unwrapping every rule
		// above is one quotation mark away from being bypassed.
		{"bash -c", `bash -c "rm -rf /"`, "recursive-root-delete"},
		{"sh -c single quoted", `sh -c 'rm -rf /'`, "recursive-root-delete"},
		{"eval", `eval "rm -rf /"`, "recursive-root-delete"},
		{"nested shells", `bash -c "sh -c 'rm -rf /'"`, "recursive-root-delete"},
		{"xargs", "xargs rm -rf /", "recursive-root-delete"},
		{"timeout with duration", "timeout 30 rm -rf /", "recursive-root-delete"},
		{"nice with flag value", "nice -n 10 rm -rf /", "recursive-root-delete"},
		{"env assignment", "env FOO=bar rm -rf /", "recursive-root-delete"},
		{"nohup wrapping delete", "nohup rm -rf / &", "recursive-root-delete"},
		{"wrapped power command", `bash -c "reboot"`, "system-power"},
		{"wrapped daemon kill", `sh -c "pkill archied"`, "daemon-self-termination"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := command.Hardline(tc.line)
			if err == nil {
				t.Fatalf("Hardline(%q) allowed the command", tc.line)
			}
			var blocked *command.Blocked
			if !errors.As(err, &blocked) {
				t.Fatalf("error type = %T, want *command.Blocked", err)
			}
			if blocked.Rule != tc.rule {
				t.Errorf("rule = %q, want %q (reason: %s)", blocked.Rule, tc.rule, blocked.Reason)
			}
			if blocked.Reason == "" {
				t.Error("refusal carries no reason, so the model cannot tell why")
			}
		})
	}
}

// TestHardlineAllows is the half that decides whether this is usable. A
// blocklist that fires on commit messages and ordinary work would be
// turned off within a day, so the quoting and command-position analysis
// matters as much as the patterns.
func TestHardlineAllows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{"echoing the string", `echo "rm -rf /"`},
		{"commit message", `git commit -m "revert the rm -rf / change"`},
		{"commit message single quotes", `git commit -m 'stop using rm -rf / in scripts'`},
		{"pr title", `gh pr create --title "guard against rm -rf /"`},
		{"grep for the pattern", `grep -r "rm -rf /" .`},
		{"writing a doc that mentions reboot", `echo "reboot the machine" > notes.txt`},

		{"ordinary recursive delete", "rm -rf ./build"},
		{"recursive delete in workspace", "rm -rf /workspace/tmp"},
		{"recursive delete of subdir", "rm -rf /var/lib/archie/work/tmp"},
		{"non-recursive root file", "rm /tmp/x"},

		{"dd to a file", "dd if=/dev/zero of=disk.img bs=1M count=10"},
		{"redirect to null", "echo x > /dev/null"},
		{"redirect to a file", "go test ./... > out.txt"},

		{"killing a pid", "kill -9 12345"},
		{"pkill something else", "pkill node"},
		{"systemctl status", "systemctl status archied"},
		{"systemctl restart something else", "systemctl restart nginx"},
		{"docker ps", "docker ps -a"},
		{"docker stop another container", "docker stop nats"},

		{"sudo without stdin", "sudo apt-get update"},
		{"ordinary build", "go build ./..."},
		{"chained ordinary commands", "cd /workspace && go test ./... && git status"},
		{"empty", ""},
		{"whitespace", "   "},

		// The unwrapping must not turn ordinary wrapped commands into
		// refusals.
		{"bash -c ordinary", `bash -c "go test ./..."`},
		{"bash -c mentioning the string", `bash -c 'echo "rm -rf /"'`},
		{"xargs ordinary", "find . -name '*.tmp' | xargs rm -f"},
		{"timeout ordinary", "timeout 30 go test ./..."},
		{"env ordinary", "env GOFLAGS=-mod=mod go build ./..."},
		{"nohup ordinary", "nohup ./long-running-job &"},
		{"deeply nested ordinary", `bash -c "sh -c 'echo hello'"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := command.Hardline(tc.line); err != nil {
				t.Errorf("Hardline(%q) blocked an ordinary command: %v", tc.line, err)
			}
		})
	}
}

// TestBlockedErrorNamesTheSegment keeps the refusal actionable: the model
// has to be able to tell which part of a chained command was objected to.
func TestBlockedErrorNamesTheSegment(t *testing.T) {
	t.Parallel()

	err := command.Hardline("go build ./... && rm -rf /etc")
	if err == nil {
		t.Fatal("Hardline allowed a recursive delete of /etc")
	}
	msg := err.Error()
	if !strings.Contains(msg, "rm -rf /etc") {
		t.Errorf("error %q does not name the offending segment", msg)
	}
	if strings.Contains(msg, "go build") {
		t.Errorf("error %q blames the wrong segment", msg)
	}
}
