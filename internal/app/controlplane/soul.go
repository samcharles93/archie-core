package controlplane

import (
	"encoding/json"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agent"
)

// SoulKind is the control-plane resource holding the agent's SOUL document.
const SoulKind = "soul"

func soulDefinition() Definition {
	return Definition{
		Kind:      SoulKind,
		Title:     "SOUL",
		Document:  agent.Soul{},
		ApplyMode: "live",
		// Seeded empty. An unowned SOUL reads the identity's file and then the
		// shipped default; the first dashboard save takes ownership.
		Seed: func(config.Config) any { return agent.Soul{} },
		Validate: func(input []byte) error {
			return validateAs(input, func(s agent.Soul) error { return s.Validate() })
		},
	}
}

func decodeSoul(input []byte) (agent.Soul, error) {
	var soul agent.Soul
	err := json.Unmarshal(input, &soul)
	return soul, err
}
