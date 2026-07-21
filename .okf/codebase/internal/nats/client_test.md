---
description: Source module internal/nats/client_test.go (118 lines).
resource: internal/nats/client_test.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: client_test.go
type: Module
---

# Module client_test.go

**Path**: `internal/nats/client_test.go`  
**Lines**: 118

## Snippet Preview

```
package nats

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
)

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	jsDir := t.TempDir()
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: jsDir}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	return srv
}

func TestConnectAndPublish(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := Connect(ctx, url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
```
