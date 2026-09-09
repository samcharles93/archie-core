package archied

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/webui"
)

// skillCatalogAdapter serves the webui's skill catalogue view from the
// runtime cataloguer. The webui deliberately does not link internal/skill
// (archie-core-8cda.5.6), so the process that owns the config and the
// runtime supplies this adapter at bootstrap.
type skillCatalogAdapter struct{ cfg *config.Holder }

var _ webui.SkillCatalog = skillCatalogAdapter{}

// Skills discovers and renders the catalogue: project, explicitly shared,
// and user-global skills; earlier roots win when names collide. A discovery
// failure renders as an empty catalogue -- the page answers "what can
// Archie do" from whatever loaded, never a failed dashboard.
func (a skillCatalogAdapter) Skills() []webui.SkillView {
	cfg := a.cfg.Get()
	roots := skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)
	catalog, err := skill.CatalogRoots(roots...)
	if err != nil {
		return []webui.SkillView{}
	}
	views := make([]webui.SkillView, 0, len(catalog))
	for _, entry := range catalog {
		views = append(views, webui.SkillView{
			Name:        entry.Name,
			Description: entry.Description,
			Workflow:    entry.Workflow,
			Source:      skillSource(entry.Root, cfg.WorkDir, cfg.SkillsDir),
		})
	}
	return views
}

// skillSource labels where a skill was loaded from.
//
// sharedDir is checked first: when it equals workDir the roots collapse to one
// entry, and matching workDir first labelled every shared skill as a project
// skill.
func skillSource(root, workDir, sharedDir string) string {
	switch root {
	case sharedDir:
		return "shared skills"
	case workDir:
		return "project skills"
	default:
		return "user-global skills"
	}
}
