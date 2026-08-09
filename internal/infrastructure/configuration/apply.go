package configuration

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/config"
)

// ApplyOverlayValues decodes overrides (a nested map of dotted-path
// values, as produced by the overlay store) into cfg using the same
// field-level precedence as a file overlay: only the keys present are
// replaced, every field the overlay omits keeps its existing value.
//
// This is the decode half of the runtime config overlay. The loader's
// ApplyOverlay wraps it with defaulting, validation and provenance; the
// dashboard PATCH path calls it on a copy of the published config and
// validates the materialised result before persisting. Either way the
// caller owns the cfg value -- pass a copy when the base must survive.
func ApplyOverlayValues(cfg *config.Config, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	data, err := yaml.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("%w: encoding config overlay: %v", ErrUnreadable, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("%w: parsing config overlay: %v", ErrUnreadable, err)
	}
	return nil
}
