---
description: Source module internal/agentexec/subprocess_test.go (247 lines).
resource: internal/agentexec/subprocess_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: subprocess_test.go
type: Module
---

# Module subprocess_test.go

**Path**: `internal/agentexec/subprocess_test.go`  
**Lines**: 247

## Snippet Preview

```
package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubprocessRunnerRoundTrip(t *testing.T) {
	req := testRequest()
	result, err := helperRunner("success").Run(context.Background(), "/workspace", req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "from child" || result.TaskID != req.TaskID || result.Attempt != req.Attempt {
		t.Fatalf("result = %#v", result)
	}
}

func TestSubprocessRunnerReturnsStampedWorkerError(t *testing.T) {
	req := testRequest()
	result, err := helperRunner("worker-error").Run(context.Background(), "/workspace", req)
	if err == nil || err.Error() != "provider unavailable" {
```
