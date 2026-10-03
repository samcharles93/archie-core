//go:build windows

package builtin

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures cmd to start in a new process group on
// Windows (see docs/specs/agents/02-spawning-and-lifecycle.md, Process
// group management / Platform behavior).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// signalProcessGroup terminates cmd's process on Windows; descendants may
// survive.
func signalProcessGroup(cmd *exec.Cmd, _ syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
