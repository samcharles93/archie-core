---
description: Source module internal/forge/github_test.go (45 lines).
resource: internal/forge/github_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: github_test.go
type: Module
---

# Module github_test.go

**Path**: `internal/forge/github_test.go`  
**Lines**: 45

## Snippet Preview

```
package forge

import (
	"io"
	"log/slog"
	"testing"
)

func TestNewConfiguresHost(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		wantBase   string
		wantUpload string
	}{
		{
			name:       "github.com",
			host:       "https://github.com",
			wantBase:   "https://api.github.com/",
			wantUpload: "https://uploads.github.com/",
		},
		{
			name:       "enterprise",
			host:       "https://git.example.com",
			wantBase:   "https://git.example.com/api/v3/",
			wantUpload: "https://git.example.com/api/uploads/",
		},
	}

	for _, tt := range tests {
```
