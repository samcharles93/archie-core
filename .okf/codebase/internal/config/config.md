---
description: Source module internal/config/config.go (424 lines).
resource: internal/config/config.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: config.go
type: Module
---

# Module config.go

**Path**: `internal/config/config.go`  
**Lines**: 424

## Snippet Preview

```
// Package config loads archied's TOML configuration. The forge API token is
// deliberately not part of the file: it comes from a configurable env var.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration that unmarshals from TOML strings ("60s").
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

```
