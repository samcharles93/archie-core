---
description: Source module internal/forge/testclient_test.go (31 lines).
resource: internal/forge/testclient_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: testclient_test.go
type: Module
---

# Module testclient_test.go

**Path**: `internal/forge/testclient_test.go`  
**Lines**: 31

## Snippet Preview

```
package forge

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v78/github"
)

// newTestClient wires a GitHubClient at a local httptest server so its
// methods can be exercised without hitting the real GitHub API.
func newTestClient(t *testing.T) (*GitHubClient, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	gh := github.NewClient(nil)
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	gh.BaseURL = base
	gh.UploadURL = base

	return &GitHubClient{gh: gh, log: slog.New(slog.NewTextHandler(io.Discard, nil))}, mux
```
