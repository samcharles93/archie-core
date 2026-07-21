---
description: Source module internal/nats/subjects.go (74 lines).
resource: internal/nats/subjects.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: subjects.go
type: Module
---

# Module subjects.go

**Path**: `internal/nats/subjects.go`  
**Lines**: 74

## Snippet Preview

```
// Package nats manages the NATS JetStream connection for task distribution
// and agent communication. When configured, the daemon publishes discovered
// issues to NATS subjects and consumes them back via a JetStream pull
// consumer. Agent workers consume stage execution requests and reply via
// core NATS inboxes. When not configured (nil client), the existing SQLite
// ClaimNext flow is used unchanged.
package nats

import (
	"fmt"
	"strings"
)

// Task distribution subjects. Each subject encodes the workflow type so
// future multi-daemon deployments can filter by workflow.
const (
	SubjectTaskBug       = "archie.task.bug"
	SubjectTaskFeature   = "archie.task.feature"
	SubjectTaskDefault   = "archie.task.default"
	SubjectTaskBootstrap = "archie.task.bootstrap"
)

// SubjectForLabels picks the NATS subject based on issue labels, mirroring
// the label-to-workflow mapping in workflow.Route.
//
//	"bug"       -> archie.task.bug
//	"feature"   -> archie.task.feature
//	"bootstrap" -> archie.task.bootstrap
//	default     -> archie.task.default
func SubjectForLabels(labels []string) string {
```
