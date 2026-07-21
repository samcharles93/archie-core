//go:build windows

package agentexec

import "os/exec"

// Windows keeps exec.CommandContext's direct-process cancellation. Production
// subprocess and container execution are currently supported on Unix hosts.
func configureProcessCancellation(_ *exec.Cmd) {}
