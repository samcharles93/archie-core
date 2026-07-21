---
description: Source module internal/forge/github.go (309 lines).
resource: internal/forge/github.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: github.go
type: Module
---

# Module github.go

**Path**: `internal/forge/github.go`  
**Lines**: 309

## Snippet Preview

```
// Package forge wraps the GitHub API surface archied needs: polling
// labelled issues, accepting collaborator invitations, commenting, and
// opening pull requests. Gitea support later lands as a second
// implementation behind the same methods.
package forge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/go-github/v78/github"
)

// GitHubClient implements Forge against the GitHub API.
type GitHubClient struct {
	gh  *github.Client
	log *slog.Logger

	labelMu       sync.Mutex
	labelsEnsured map[string]bool
}

// New creates a GitHub-backed Forge implementation. Non-github.com hosts
// are configured using GitHub Enterprise's API and upload URL conventions.
func New(token, host string, log *slog.Logger) (Forge, error) {
	client := github.NewClient(nil)
	if strings.TrimRight(host, "/") != "https://github.com" {
```
