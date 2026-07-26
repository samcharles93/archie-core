// Package memory adapts the memory manager to the typed tool-provider family.
package memory

import (
	"context"
	"errors"

	corememory "github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Provider exposes a memory manager's executable tool schemas.
type Provider struct {
	manager *corememory.Manager
}

// New creates a memory tool provider.
func New(manager *corememory.Manager) *Provider {
	return &Provider{manager: manager}
}

// Manifest declares the adapter's tool capability.
func (p *Provider) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "memory.tools",
		Name:         "Memory tools",
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
	}
}

// Start is a no-op because the composition root owns memory initialization.
func (p *Provider) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.manager == nil {
		return errors.New("memory tool provider manager is nil")
	}
	return nil
}

// Discover returns executable memory tools.
func (p *Provider) Discover(ctx context.Context) ([]tools.ToolEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.manager == nil {
		return nil, errors.New("memory tool provider manager is nil")
	}
	return p.manager.GetToolSchemas(), nil
}

// Health reports whether a manager is configured.
func (p *Provider) Health(context.Context) plugin.Health {
	if p.manager == nil {
		return plugin.Health{
			Status:  plugin.HealthUnhealthy,
			Message: "memory manager is not configured",
		}
	}
	if p.manager.HasShutdown() {
		return plugin.Health{
			Status:  plugin.HealthUnhealthy,
			Message: "memory manager is shut down",
		}
	}
	return plugin.Health{Status: plugin.HealthHealthy}
}

// Stop is a no-op because the daemon owns memory shutdown.
func (p *Provider) Stop(context.Context) error {
	return nil
}
