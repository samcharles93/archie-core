package configuration

import (
	"fmt"
	"strings"
)

// Role describes why a file was read, so provenance distinguishes a value
// the operator set in the base config from one an overlay replaced.
type Role string

const (
	RoleMain    Role = "main"    // config.yaml / config.toml
	RoleFeature Role = "feature" // config.<feature>.yaml or conf.d/<feature>.yaml
	RoleExtra   Role = "extra"   // conf.d/*.yaml for an unrecognised name
)

// Layer distinguishes the base configuration from an overlay applied on top.
type Layer string

const (
	LayerBase    Layer = "base"
	LayerOverlay Layer = "overlay"
)

// Origin records one file that contributed to a loaded configuration.
type Origin struct {
	Path    string
	Role    Role
	Layer   Layer
	Feature Feature // set when Role is RoleFeature or RoleExtra
}

// String renders an origin for logs and error messages.
func (o Origin) String() string {
	if o.Feature != "" {
		return fmt.Sprintf("%s (%s %s %s)", o.Path, o.Layer, o.Role, o.Feature)
	}
	return fmt.Sprintf("%s (%s %s)", o.Path, o.Layer, o.Role)
}

// Provenance is the ordered list of files that produced a configuration,
// earliest first, so a later entry overrides an earlier one.
//
// Nothing recorded where a value came from before. With overlays, feature
// files and conf.d/ all able to set the same field, "which file set this?"
// was answerable only by re-deriving the precedence rules by hand.
type Provenance struct {
	Origins []Origin
}

// record appends an origin.
func (p *Provenance) record(o Origin) { p.Origins = append(p.Origins, o) }

// Paths returns the contributing file paths in precedence order.
func (p *Provenance) Paths() []string {
	paths := make([]string, 0, len(p.Origins))
	for _, o := range p.Origins {
		paths = append(paths, o.Path)
	}
	return paths
}

// String renders the full chain for diagnostics.
func (p *Provenance) String() string {
	if len(p.Origins) == 0 {
		return "(no configuration files)"
	}
	parts := make([]string, 0, len(p.Origins))
	for _, o := range p.Origins {
		parts = append(parts, o.String())
	}
	return strings.Join(parts, " -> ")
}
