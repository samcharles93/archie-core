//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package agentexec

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessCancellation puts the worker and every command it starts in
// a dedicated process group. CommandContext cancellation then kills the whole
// group so toolchain grandchildren cannot continue mutating the worktree.
func configureProcessCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
