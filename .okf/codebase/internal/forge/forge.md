---
description: Source module internal/forge/forge.go (56 lines).
resource: internal/forge/forge.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: forge.go
type: Module
---

# Module forge.go

**Path**: `internal/forge/forge.go`  
**Lines**: 56

## Snippet Preview

```
// Package forge defines the interface archie uses to interact with a git
// host — polling issues, managing labels, opening PRs, and reacting to
// comments. GitHub is the primary implementation; other hosts (Gitea,
// GitLab) can be added later by implementing this interface.
package forge

import "context"

// Issue is a forge-neutral representation of an issue (not a PR).
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// Forge is the interface for interacting with a git host.
type Forge interface {
	// AcceptInvitations auto-accepts pending repository invitations.
	AcceptInvitations(ctx context.Context) error

	// AssignedIssues returns open issues assigned to the given user,
	// excluding PRs.
	AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error)

	// IssuesWithLabel returns open issues matching the given label,
	// excluding PRs. Used when Dispatch.Trigger is "label" or "either".
	IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error)

	// Comment posts an issue (or PR) comment and returns its id.
```
