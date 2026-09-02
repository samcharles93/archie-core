package archied

import (
	"fmt"
	"slices"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
	"github.com/samcharles93/archie-core/internal/webui"
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

// applyRepoFieldUpdate returns a copy of repos with one editable field
// changed on the repository matched by owner/name (archie-core-b6ew.6).
// repos must already be a caller-owned copy (config.Config.Clone's
// Repos) -- this mutates the matched element in place and returns the
// same slice, matching applyDottedOverlay's "caller owns the copy"
// contract. Kept as a pure function, separate from
// installUpdateRepoFieldHandler's closure, so the field-matching and
// type-coercion logic is directly unit-testable without constructing a
// full boot.
func applyRepoFieldUpdate(repos []config.Repo, owner, name, field string, value any) ([]config.Repo, error) {
	if !webui.RepoEditableFields[field] {
		return nil, fmt.Errorf("%w: repo field %s is not editable from the dashboard", webui.ErrConfigUpdateInvalid, field)
	}
	index := slices.IndexFunc(repos, func(r config.Repo) bool { return r.Owner == owner && r.Name == name })
	if index < 0 {
		return nil, fmt.Errorf("%w: repository %s/%s is not configured", webui.ErrConfigUpdateInvalid, owner, name)
	}
	switch field {
	case "allow_concurrent":
		v, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: allow_concurrent must be a boolean", webui.ErrConfigUpdateInvalid)
		}
		repos[index].AllowConcurrent = v
	case "review_enabled":
		v, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: review_enabled must be a boolean", webui.ErrConfigUpdateInvalid)
		}
		repos[index].ReviewEnabled = v
	case "max_retries":
		v, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("%w: max_retries must be a number", webui.ErrConfigUpdateInvalid)
		}
		repos[index].MaxRetries = int(v)
	}
	return repos, nil
}
