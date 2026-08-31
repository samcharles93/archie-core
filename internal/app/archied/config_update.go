package archied

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
)

// applyDottedOverlay decodes dotted-path updates into cfg. yaml cannot
// decode dotted keys ("budgets.max_steps" is not a struct field), so
// the updates are nested into a map first -- the same nesting the
// overlay store's Snapshot performs -- then decoded with the same
// field-level merge the file overlay uses. cfg must be a copy the
// caller owns (a Clone of the published config); it is never decoded
// into a published snapshot directly.
func applyDottedOverlay(cfg *config.Config, updates map[string]any) error {
	nested := make(map[string]any)
	for key, value := range updates {
		if err := overlay.Nest(nested, key, value); err != nil {
			return err
		}
	}
	return configuration.ApplyOverlayValues(cfg, nested)
}
