---
description: Source module internal/store/events.go (243 lines).
resource: internal/store/events.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: events.go
type: Module
---

# Module events.go

**Path**: `internal/store/events.go`  
**Lines**: 243

## Snippet Preview

```
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

const eventsSchema = `
CREATE TABLE IF NOT EXISTS events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	at        TEXT NOT NULL,
	kind      TEXT NOT NULL,
	task_id   INTEGER NOT NULL DEFAULT 0,
	repo      TEXT NOT NULL DEFAULT '',
	issue     INTEGER NOT NULL DEFAULT 0,
	workflow  TEXT NOT NULL DEFAULT '',
	stage     TEXT NOT NULL DEFAULT '',
	detail    TEXT NOT NULL DEFAULT '',
	data      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, id);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind, id);
`

```
