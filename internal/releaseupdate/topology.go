package releaseupdate

import "fmt"

type Topology struct {
	Units          []string `json:"units"`
	ConfigSections []string `json:"config_sections"`
}

// MigrationPlan preserves the release's dependency order. Unknown observations
// require every step to be checked manually rather than assuming a partial host.
func (t Topology) MigrationPlan(units, sections map[string]bool) []string {
	var steps []string
	for _, section := range t.ConfigSections {
		if !sections[section] {
			steps = append(steps, fmt.Sprintf("configure [%s]", section))
		}
	}
	for _, unit := range t.Units {
		if !units[unit] {
			steps = append(steps, fmt.Sprintf("install and enable %s.service", unit))
		}
	}
	return steps
}
