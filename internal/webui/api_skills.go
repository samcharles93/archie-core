package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/skill"
)

// SkillView is one catalogued skill as shown on the dashboard's Skills page:
// what it does, in plain language, and where it was discovered.
type SkillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Workflow    string `json:"workflow,omitempty"`
	Source      string `json:"source"`
}

// handleSkills reports the skill catalogue -- what Archie can actually do,
// beyond what is in the source. It answers the same question a fresh
// operator would ask by reading internal/skill/, without the reading.
//
// The catalogue layers project, explicitly shared, and user-global skills.
// Earlier roots win when names collide. Missing roots are legitimate and
// produce an empty or partial catalogue rather than a failed dashboard.
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if s.Cfg == nil {
		writeJSON(w, map[string]any{"skills": []SkillView{}})
		return
	}

	cfg := s.Cfg.Get()
	roots := skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)
	catalog, err := skill.CatalogRoots(roots...)
	if err != nil {
		http.Error(w, "failed to read skill catalogue: "+err.Error(), http.StatusInternalServerError)
		return
	}

	views := make([]SkillView, 0, len(catalog))
	for _, entry := range catalog {
		views = append(views, SkillView{
			Name:        entry.Name,
			Description: entry.Description,
			Workflow:    entry.Workflow,
			Source:      skillSource(entry.Root, cfg.WorkDir, cfg.SkillsDir),
		})
	}

	// Each skill already carries its own Source, which is what the page
	// renders. A top-level dir/dirs pair had no consumer and implied the
	// catalog came from one place when it is merged from several.
	writeJSON(w, map[string]any{"skills": views})
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
