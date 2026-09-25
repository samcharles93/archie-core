//go:build unix

package agentexec

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the harness in its own process group and makes
// cancellation kill the whole group.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
