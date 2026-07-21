---
description: Source module internal/agentexec/inprocess_test.go (173 lines).
resource: internal/agentexec/inprocess_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: inprocess_test.go
type: Module
---

# Module inprocess_test.go

**Path**: `internal/agentexec/inprocess_test.go`  
**Lines**: 173

## Snippet Preview

```
package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/ai-sdk/agentloop"
	"github.com/samcharles93/ai-sdk/runtime"
)

func TestInProcessRunnerMapsRequestAndCapturesOutput(t *testing.T) {
	runner := &InProcessRunner{
		runtime: runtime.NewRuntime(runtime.Config{}),
		run: func(ctx context.Context, cfg agentloop.Config) (agentloop.Result, error) {
			if cfg.WorkDir != "/workspace" || cfg.ModelRef != "provider/model" || cfg.Mission != "mission" {
				t.Fatalf("unexpected config: workdir=%q model=%q mission=%q", cfg.WorkDir, cfg.ModelRef, cfg.Mission)
			}
			if cfg.ProtectPaths == nil || !cfg.ProtectPaths("nested/file_test.go") || !cfg.ProtectPaths("view_templ.go") {
				t.Fatal("declarative path protection was not applied")
			}
			if err := cfg.Notes.Append(ctx, "verified note"); err != nil {
				t.Fatal(err)
			}
			if _, err := cfg.Extra["decide"].Execute(ctx, `{"fit":true}`); err != nil {
				t.Fatal(err)
			}
```
