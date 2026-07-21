---
description: Source module internal/agentexec/runtime.go (23 lines).
resource: internal/agentexec/runtime.go
tags:
    - go
    - source
timestamp: "2026-07-21T18:36:22Z"
title: runtime.go
type: Module
---

# Module runtime.go

**Path**: `internal/agentexec/runtime.go`  
**Lines**: 23

## Snippet Preview

```
package agentexec

import "github.com/samcharles93/ai-sdk/runtime"

// NewRuntime builds the provider runtime shared by archied's in-process mode
// and the archie-agent worker. Nil means no providers were configured.
func NewRuntime(providers map[string]Provider) *runtime.Runtime {
	if len(providers) == 0 {
		return nil
	}
	runtime.RegisterBuiltinClasses()
	catalog := make(map[string]runtime.ProviderConfig, len(providers))
	for name, provider := range providers {
		cfg := runtime.ProviderConfig{ID: name, Class: provider.Class, BaseURL: provider.BaseURL}
		if provider.APIKeyEnv == "" {
			cfg.Auth = runtime.AuthConfig{Type: runtime.AuthTypeNone}
		} else {
			cfg.Auth = runtime.AuthConfig{APIKeyEnv: provider.APIKeyEnv}
		}
		catalog[name] = cfg
	}
	return runtime.NewRuntime(runtime.Config{Providers: catalog})
}
```
