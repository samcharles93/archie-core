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
// The catalogue is scanned from cfg.SkillsDir when configured (a shared
// directory meant to be pointed at deliberately), falling back to
// cfg.WorkDir's own .agents/skills/ otherwise. Both are legitimate --
// skill.Catalog treats a missing directory as "no skills," not an error --
// so an unconfigured deployment gets an empty, explained list rather than a
// failure.
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if s.Cfg == nil {
		writeJSON(w, map[string]any{"skills": []SkillView{}, "dir": ""})
		return
	}

	dir := s.Cfg.SkillsDir
	source := "shared skills directory"
	if dir == "" {
		dir = s.Cfg.WorkDir
		source = "bundled with this project"
	}

	catalog, err := skill.Catalog(dir)
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
			Source:      source,
		})
	}

	writeJSON(w, map[string]any{"skills": views, "dir": dir})
}
