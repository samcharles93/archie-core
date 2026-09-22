// Package toolbuilder resolves a curator's declared tool names from the
// daemon's process-wide tool registry (internal/tools.Registry). It is the
// implementation of curator.ToolBuilder: resolution returns exactly the
// declared set, never a broader registry, and a declared name with no
// implementation fails the build rather than silently returning fewer tools.
package toolbuilder

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/tools"
)

var _ curator.ToolBuilder = (*Builder)(nil)

// Builder resolves declared curator tool names against a tools.Registry.
type Builder struct {
	reg *tools.Registry
}

// New builds a ToolBuilder over reg. A nil reg is valid and reports a
// clear error from Build when a declared tool set is requested.
func New(reg *tools.Registry) *Builder {
	return &Builder{reg: reg}
}

// Build returns exactly the declared set in declared order. A declared name
// absent from the registry fails the build.
func (b *Builder) Build(_ context.Context, declared []string) ([]tools.ToolEntry, error) {
	if b.reg == nil {
		if len(declared) == 0 {
			return nil, nil
		}
		return nil, errors.New("toolbuilder: no tool registry bound")
	}
	out := make([]tools.ToolEntry, 0, len(declared))
	for _, name := range declared {
		entry, ok := b.reg.Get(name)
		if !ok {
			return nil, fmt.Errorf("toolbuilder: declared tool %q has no implementation", name)
		}
		out = append(out, entry)
	}
	return out, nil
}
