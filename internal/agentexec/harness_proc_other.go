//go:build !unix

package agentexec

import (
	"errors"
	"os/exec"
)

// setProcessGroup falls back to killing the harness process alone where
// process groups are unavailable.
func setProcessGroup(*exec.Cmd) {}

// runAsHarnessUser refuses a named harness user where user switching is
// unavailable, rather than running the harness as the worker.
func runAsHarnessUser(_ *exec.Cmd, name string) error {
	if name != "" {
		return errors.New("harness user switching is unsupported on this platform")
	}
	return nil
}
