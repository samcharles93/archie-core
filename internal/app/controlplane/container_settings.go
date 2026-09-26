package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// containerRuntimePolicies is the container-runtime-policies resource document.
// Its shape IS the contract the Web UI edits, so it is defined here with
// explicit snake_case json tags and never handed the internal config.ContainerConfig
// directly: that struct's tags belong to the TOML file format, and when it was
// the document, encoding/json fell back to its Go field names -- "Image",
// "MaxConcurrency", "LegacyEnabled" -- the only Go-cased resource left in the
// registry (archie-core-1171). The two durations are config.Duration, which
// already writes the string the file accepts; the leak was the keys only.
type containerRuntimePolicies struct {
	Image          string                  `json:"image"`
	MaxConcurrency int                     `json:"max_concurrency"`
	MaxUptime      config.Duration         `json:"max_uptime"`
	VolumeTTL      config.Duration         `json:"volume_ttl"`
	PullPolicy     string                  `json:"pull_policy"`
	Network        string                  `json:"network"`
	Profiles       map[string]agentProfile `json:"profiles,omitempty"`
}

// agentProfile is one named execution environment as the document writes it.
type agentProfile struct {
	Image   string   `json:"image,omitempty"`
	Kit     []string `json:"kit,omitempty"`
	Adapter string   `json:"adapter,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

// legacyContainerKeys are the Go field names encoding/json fell back to while
// the document was the internal config struct. The decoder's case-insensitive
// fallback compares whole keys, so "MaxConcurrency" never matches
// "max_concurrency"; every store written before the reshape holds these keys,
// so reading them is deliberate and everything else stays strict.
var legacyContainerKeys = map[string]string{
	"Image": "image", "MaxConcurrency": "max_concurrency", "MaxUptime": "max_uptime",
	"VolumeTTL": "volume_ttl", "PullPolicy": "pull_policy", "Network": "network",
	"Profiles": "profiles",
}

// legacyProfileKeys are the same fallback at the profile nesting level.
var legacyProfileKeys = map[string]string{
	"Image": "image", "Kit": "kit", "Adapter": "adapter", "Tools": "tools",
}

func (p *agentProfile) UnmarshalJSON(data []byte) error {
	type document agentProfile
	var decoded document
	err := controlplanerpc.DecodeRenamingLegacyKeys(data, legacyProfileKeys, &decoded)
	if err != nil {
		return err
	}
	*p = agentProfile(decoded)
	return nil
}

func (p *containerRuntimePolicies) UnmarshalJSON(data []byte) error {
	type document containerRuntimePolicies
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	// "LegacyEnabled" decoded the removed containers.enabled switch during the
	// window in which this resource existed, and stores written then hold it.
	// It has no counterpart in the current shape -- it never had a runtime
	// consumer -- so it is dropped rather than renamed: the strict decode would
	// otherwise refuse a document every deployment of that window stores.
	delete(raw, "LegacyEnabled")
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var decoded document
	if err := controlplanerpc.DecodeRenamingLegacyKeys(encoded, legacyContainerKeys, &decoded); err != nil {
		return err
	}
	*p = containerRuntimePolicies(decoded)
	return nil
}

// settings maps the document onto the internal config struct the daemon runs.
func (p containerRuntimePolicies) settings() config.ContainerConfig {
	out := config.ContainerConfig{
		Image: p.Image, MaxConcurrency: p.MaxConcurrency, MaxUptime: p.MaxUptime,
		VolumeTTL: p.VolumeTTL, PullPolicy: p.PullPolicy, Network: p.Network,
	}
	if p.Profiles != nil {
		out.Profiles = make(map[string]config.AgentProfile, len(p.Profiles))
		for name, profile := range p.Profiles {
			out.Profiles[name] = config.AgentProfile{Image: profile.Image, Kit: profile.Kit, Adapter: profile.Adapter, Tools: profile.Tools}
		}
	}
	return out
}

func seedContainerPolicies(cfg config.Config) any {
	containers := cfg.Containers
	policies := containerRuntimePolicies{
		Image: containers.Image, MaxConcurrency: containers.MaxConcurrency, MaxUptime: containers.MaxUptime,
		VolumeTTL: containers.VolumeTTL, PullPolicy: containers.PullPolicy, Network: containers.Network,
	}
	if containers.Profiles != nil {
		policies.Profiles = make(map[string]agentProfile, len(containers.Profiles))
		for name, profile := range containers.Profiles {
			policies.Profiles[name] = agentProfile{Image: profile.Image, Kit: profile.Kit, Adapter: profile.Adapter, Tools: profile.Tools}
		}
	}
	return policies
}

func validateContainers(input []byte) error {
	return validateAs(input, func(policies containerRuntimePolicies) error {
		settings := policies.settings()
		if settings.MaxConcurrency < 0 || settings.MaxUptime < 0 || settings.VolumeTTL < 0 {
			return fmt.Errorf("container limits must not be negative")
		}
		if settings.PullPolicy != "" && settings.PullPolicy != "missing" && settings.PullPolicy != "always" {
			return fmt.Errorf("pull_policy must be missing or always")
		}
		// The image is required for autonomous workflow workers; the same rule
		// holds for the stored policies that replace the file's [containers].
		if strings.TrimSpace(settings.Image) == "" {
			return fmt.Errorf("container image is required for autonomous workflow workers")
		}
		return settings.ValidateProfiles()
	})
}

// normalizeContainerPolicies canonicalizes the stored document: decoding into
// the shape above and re-encoding writes the snake_case keys, dropping the
// Go-cased keys an earlier revision stored. A document that still carries them
// converges the next time it is written; reads tolerate them meanwhile
// (containerRuntimePolicies), so nothing has to be re-saved before it can be
// read again.
func normalizeContainerPolicies(input []byte) ([]byte, error) {
	var policies containerRuntimePolicies
	if err := json.Unmarshal(input, &policies); err != nil {
		return nil, err
	}
	return json.Marshal(policies)
}
