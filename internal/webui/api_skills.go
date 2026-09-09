package webui

import "net/http"

// SkillView is one catalogued skill as shown on the dashboard's Skills page:
// what it does, in plain language, and where it was discovered.
type SkillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Workflow    string `json:"workflow,omitempty"`
	Source      string `json:"source"`
}

// SkillCatalog is the webui-owned source of the dashboard's skill
// catalogue. It is deliberately a view over already-discovered entries, not
// the discovery machinery: the cataloguer (internal/skill) drags the agent
// toolchain in, and the UI process must not link it (archie-core-8cda.5.6).
// Whoever owns the runtime -- the daemon, via its config -- supplies an
// adapter at bootstrap; an unwired catalog degrades to an empty page.
type SkillCatalog interface {
	Skills() []SkillView
}

// handleSkills reports the skill catalogue -- what Archie can actually do,
// beyond what is in the source. It answers the same question a fresh
// operator would ask by reading internal/skill/, without the reading.
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if s.Skills == nil {
		writeJSON(w, map[string]any{"skills": []SkillView{}})
		return
	}
	writeJSON(w, map[string]any{"skills": s.Skills.Skills()})
}
