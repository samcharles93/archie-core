package controlplane

import "github.com/samcharles93/archie-core/internal/config"

// AgentProfileKind is the control-plane resource holding agent profiles. Its
// document is the snake_case agentProfile wire type, not config.AgentProfile,
// which has no json tags.
const AgentProfileKind = "agent-profiles"

func agentProfileDefinition() Definition {
	return Definition{
		Kind:      AgentProfileKind,
		Title:     "Agent profiles",
		ApplyMode: "live",
		Document:  map[string]agentProfile{},
		Seed:      seedAgentProfiles,
		Validate:  validateAgentProfiles,
	}
}

func seedAgentProfiles(cfg config.Config) any {
	return agentProfilesDocument(cfg.Containers.Profiles)
}

// agentProfilesDocument maps the internal type to the document's wire type.
func agentProfilesDocument(profiles map[string]config.AgentProfile) map[string]agentProfile {
	if profiles == nil {
		return nil
	}
	out := make(map[string]agentProfile, len(profiles))
	for name, p := range profiles {
		out[name] = agentProfile{Image: p.Image, Kit: p.Kit, Adapter: p.Adapter, Tools: p.Tools}
	}
	return out
}

// agentProfilesSettings reverses agentProfilesDocument, for the layering in
// runtime_config.go and for validation against config.ValidateAgentProfiles.
func agentProfilesSettings(profiles map[string]agentProfile) map[string]config.AgentProfile {
	if profiles == nil {
		return nil
	}
	out := make(map[string]config.AgentProfile, len(profiles))
	for name, p := range profiles {
		out[name] = config.AgentProfile{Image: p.Image, Kit: p.Kit, Adapter: p.Adapter, Tools: p.Tools}
	}
	return out
}

func validateAgentProfiles(input []byte) error {
	// The rules live in the config package and are shared with the file
	// document's own validation (config.ContainerConfig.ValidateProfiles):
	// which profiles are valid cannot differ between the file that seeds
	// this resource and the resource that replaces it.
	return validateAs(input, func(profiles map[string]agentProfile) error {
		return config.ValidateAgentProfiles(agentProfilesSettings(profiles))
	})
}
