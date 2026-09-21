package controlplane

import (
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

const (
	objectSchema = `{"type":"object"}`
	arraySchema  = `{"type":"array"}`
)

func builtinDefinitions(steps workflow.StepRegistry) []Definition {
	definitions := []Definition{workflowDefinition(), workflowDefinitionsDefinition(steps), personaDefinition(), scheduleDefinition()}
	definitions = append(definitions, modelDefinitions()...)
	definitions = append(definitions, operationalDefinitions()...)
	return definitions
}

func domainManagedDescriptors() []*pb.ResourceDescriptor {
	const stateService = "state.v1.StateStoreService"
	return []*pb.ResourceDescriptor{
		{Kind: "identities", Title: "Identities", ApplyMode: "domain-managed", QueryService: stateService + "/ListIdentities,GetIdentity", CommandService: stateService + "/CreateIdentity,RenameIdentity,SuspendIdentity,ReactivateIdentity,RetireIdentity"},
		{Kind: "capture-events", Title: "Captured events", ApplyMode: "domain-managed", QueryService: stateService + "/StreamCaptures", CommandService: stateService + "/InsertCapture"},
		{Kind: "capture-mappings", Title: "Capture mappings", ApplyMode: "domain-managed", QueryService: stateService + "/ListMappings", CommandService: stateService + "/InsertMapping,UpdateMapping,DeleteMapping"},
		{Kind: "capture-bindings", Title: "Capture bindings", ApplyMode: "domain-managed", QueryService: stateService + "/ListBindings", CommandService: stateService + "/InsertBinding,UpdateBinding,DeleteBinding"},
	}
}
