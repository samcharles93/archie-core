package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// containerRuntimePolicies is the container-runtime-policies document, with
// snake_case JSON keys.
type containerRuntimePolicies struct {
	Image          string          `json:"image"`
	MaxConcurrency int             `json:"max_concurrency"`
	MaxUptime      config.Duration `json:"max_uptime"`
	VolumeTTL      config.Duration `json:"volume_ttl"`
	PullPolicy     string          `json:"pull_policy"`
	Network        string          `json:"network"`
}

// agentProfile is one named execution environment as the document writes it.
type agentProfile struct {
	Image   string   `json:"image,omitempty"`
	Kit     string   `json:"kit,omitempty"`
	Adapter string   `json:"adapter,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

// legacyContainerKeys maps Go-cased keys from older stored documents to
// their current names.
var legacyContainerKeys = map[string]string{
	"Image": "image", "MaxConcurrency": "max_concurrency", "MaxUptime": "max_uptime",
	"VolumeTTL": "volume_ttl", "PullPolicy": "pull_policy", "Network": "network",
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
	// LegacyEnabled has no current field; drop it.
	delete(raw, "LegacyEnabled")
	// "profiles"/"Profiles" carried config.ContainerConfig.Profiles before it
	// moved to its own resource, AgentProfileKind; a document stored under
	// either name before that still decodes, with the key simply dropped.
	delete(raw, "profiles")
	delete(raw, "Profiles")
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
// Profiles is never set here: config.ContainerConfig.Profiles is json:"-",
// and AgentProfileKind's own layering step in runtime_config.go is what sets
// it, after this one runs.
func (p containerRuntimePolicies) settings() config.ContainerConfig {
	return config.ContainerConfig{
		Image: p.Image, MaxConcurrency: p.MaxConcurrency, MaxUptime: p.MaxUptime,
		VolumeTTL: p.VolumeTTL, PullPolicy: p.PullPolicy, Network: p.Network,
	}
}

func seedContainerPolicies(cfg config.Config) any {
	containers := cfg.Containers
	return containerRuntimePolicies{
		Image: containers.Image, MaxConcurrency: containers.MaxConcurrency, MaxUptime: containers.MaxUptime,
		VolumeTTL: containers.VolumeTTL, PullPolicy: containers.PullPolicy, Network: containers.Network,
	}
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
		// Profiles is json:"-" on config.ContainerConfig: it is
		// AgentProfileKind's document now, validated there.
		return nil
	})
}

// normalizeContainerPolicies re-encodes the stored document with snake_case
// keys.
func normalizeContainerPolicies(input []byte) ([]byte, error) {
	var policies containerRuntimePolicies
	if err := json.Unmarshal(input, &policies); err != nil {
		return nil, err
	}
	return json.Marshal(policies)
}
