---
description: Source module internal/agentexec/subprocess_process_windows.go (9 lines).
resource: internal/agentexec/subprocess_process_windows.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: subprocess_process_windows.go
type: Module
---

# Module subprocess_process_windows.go

**Path**: `internal/agentexec/subprocess_process_windows.go`  
**Lines**: 9

## Snippet Preview

```
//go:build windows

package agentexec

import "os/exec"

// Windows keeps exec.CommandContext's direct-process cancellation. Production
// subprocess and container execution are currently supported on Unix hosts.
func configureProcessCancellation(_ *exec.Cmd) {}
```
