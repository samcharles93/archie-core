package controlplane

import (
	"encoding/json"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agent"
)

const PersonasKind = "personas"

func personaDefinition() Definition {
	return Definition{
		Kind:      PersonasKind,
		Title:     "Personas",
		Document:  agent.PersonaCollection{},
		ApplyMode: "live",
		Seed:      func(config.Config) any { return agent.ShippedPersonas() },
		Validate: func(input []byte) error {
			return validateAs(input, func(collection agent.PersonaCollection) error { return collection.Validate() })
		},
	}
}

func decodePersonas(input []byte) (agent.PersonaCollection, error) {
	var collection agent.PersonaCollection
	err := json.Unmarshal(input, &collection)
	return collection, err
}
