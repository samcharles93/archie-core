---
description: Source module internal/agentexec/subprocess_process_unix.go (27 lines).
resource: internal/agentexec/subprocess_process_unix.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: subprocess_process_unix.go
type: Module
---

# Module subprocess_process_unix.go

**Path**: `internal/agentexec/subprocess_process_unix.go`  
**Lines**: 27

## Snippet Preview

```
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
```
