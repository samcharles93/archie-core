---
description: Source module internal/workflow/feasibility.go (168 lines).
resource: internal/workflow/feasibility.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: feasibility.go
type: Module
---

# Module feasibility.go

**Path**: `internal/workflow/feasibility.go`  
**Lines**: 168

## Snippet Preview

```
package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/store"
)

// Feasibility is the feature-request workflow: assess the request
// against the project's direction, close it with reasons when it
// doesn't fit, otherwise produce a PRD, deliver it to Sam (issue
// comment + notify webhook), and hand the task to waiting_human. The
// daemon watches for Sam's reply and — LLM-judged, not keyword-matched —
// requeues approved features under the implement workflow or closes
// rejected ones. Routed via the "feature" label.
func Feasibility() Workflow {
	return Workflow{
		Name: "feasibility",
		Stages: []Stage{
			StagePrepareWorktree(), // read-only stages still need the checkout

			AgentStage{
				Name:         "assess",
				Role:         "planner",
```
