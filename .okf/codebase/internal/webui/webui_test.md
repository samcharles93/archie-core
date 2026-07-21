---
description: Source module internal/webui/webui_test.go (180 lines).
resource: internal/webui/webui_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: webui_test.go
type: Module
---

# Module webui_test.go

**Path**: `internal/webui/webui_test.go`  
**Lines**: 180

## Snippet Preview

```
package webui

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &Server{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestHandleSummary(t *testing.T) {
```
