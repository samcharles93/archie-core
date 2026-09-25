//go:build !unix

package agentexec

import "os/exec"

// setProcessGroup falls back to killing the harness process alone where
// process groups are unavailable.
func setProcessGroup(*exec.Cmd) {}
