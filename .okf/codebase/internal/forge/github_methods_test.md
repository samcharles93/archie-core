---
description: Source module internal/forge/github_methods_test.go (359 lines).
resource: internal/forge/github_methods_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: github_methods_test.go
type: Module
---

# Module github_methods_test.go

**Path**: `internal/forge/github_methods_test.go`  
**Lines**: 359

## Snippet Preview

```
package forge

import (
	"encoding/json"
	"net/http"
	"testing"
)

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func TestAssignedIssuesExcludesPRs(t *testing.T) {
	c, mux := newTestClient(t)
	mux.HandleFunc("GET /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("assignee"); got != "archie-bot" {
			t.Errorf("assignee = %q", got)
		}
		writeJSON(t, w, []map[string]any{
			{"number": 1, "title": "real issue", "body": "b", "labels": []map[string]any{{"name": "bug"}}},
			{"number": 2, "title": "a pr", "pull_request": map[string]any{"url": "x"}},
		})
	})

	issues, err := c.AssignedIssues(t.Context(), "o", "r", "archie-bot")
	if err != nil {
```
