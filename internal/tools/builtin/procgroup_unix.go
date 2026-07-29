// Lifted from tau internal/agent/tools/procgroup_unix.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
//go:build !windows

package builtin

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures cmd to start as the leader of a new process
// group, so the parent can signal the whole tree (see
// docs/specs/agents/02-spawning-and-lifecycle.md, Process group
// management). Must be called before cmd.Start().
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalProcessGroup sends sig to cmd's entire process group (shells,
// tools, provider subprocesses, grandchild agents - everything that
// inherited the group from setProcessGroup). Must be called after
// cmd.Start(). ESRCH (group already gone) is not an error worth surfacing.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	// A negative pid targets the whole process group (see kill(2)).
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
