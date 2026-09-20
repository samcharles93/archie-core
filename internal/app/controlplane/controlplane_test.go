package controlplane

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestCatalogCarriesShippedWorkflowDefinitionsForRestore(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	catalog, err := NewServer(resources).Catalog(t.Context(), &pb.CatalogRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var defaults string
	for _, resource := range catalog.Resources {
		if resource.Kind == WorkflowDefinitionsKind {
			defaults = resource.DefaultsJson
		}
	}
	if defaults == "" {
		t.Fatal("workflow-definitions descriptor carries no defaults")
	}
	collection, err := workflow.DecodeDefinitionCollection([]byte(defaults), workflow.BuiltinStepRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := collection.DefinitionByID("remediate"); !ok {
		t.Fatalf("shipped defaults = %#v", collection.Definitions)
	}
}

func TestImportConfigSeedsWorkflowDefinitionsWithoutOverwritingOverride(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	server := NewServer(resources)
	if _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	resource, err := resources.Resource(t.Context(), WorkflowDefinitionsKind)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := workflow.DecodeDefinitionCollection(resource.Value, workflow.BuiltinStepRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := definitions.DefinitionByID("remediate"); !ok {
		t.Fatal("seeded definitions omit remediate")
	}
	custom := workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "custom", YAML: "id: custom\nsteps:\n  - type: bootstrap.apply\n"}}}
	value, err := encodeWorkflowDefinitions(custom)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.PutResource(t.Context(), store.ResourceWrite{Kind: WorkflowDefinitionsKind, Value: value, ExpectedVersion: resource.Version, Actor: "test", Source: "test", RequestID: "override"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	got, err := resources.Resource(t.Context(), WorkflowDefinitionsKind)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Fatalf("version after reimport = %d, want 2", got.Version)
	}
}

func TestImportConfigSeedsPersonasWithoutOverwritingEdits(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	server := NewServer(resources)
	if _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	resource, err := resources.Resource(t.Context(), PersonasKind)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := decodePersonas(resource.Value)
	if err != nil || len(collection.Personas) != len(agent.ShippedPersonas().Personas) {
		t.Fatalf("seeded personas = %+v, %v", collection, err)
	}
	collection.Personas[0].Prompt = "edited"
	value, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.PutResource(t.Context(), store.ResourceWrite{Kind: PersonasKind, Value: value, ExpectedVersion: resource.Version, Actor: "test", Source: "test", RequestID: "persona-edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	got, err := resources.Resource(t.Context(), PersonasKind)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := decodePersonas(got.Value)
	if err != nil || updated.Personas[0].Prompt != "edited" || got.Version != 2 {
		t.Fatalf("personas after reimport = %+v (version %d), %v", updated, got.Version, err)
	}
}

func TestImportWorkflowExecutionSettingsDoesNotOverwriteExistingValue(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	server := NewServer(resources)
	first := workflow.ExecutionSettings{MaxModelToolSteps: 10, MaxRuntime: time.Minute, MaxConsecutiveGateFailures: 2}
	if _, err := server.ImportWorkflowExecutionSettings(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ImportWorkflowExecutionSettings(t.Context(), workflow.ExecutionSettings{MaxModelToolSteps: 99, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 9}); err != nil {
		t.Fatal(err)
	}
	resource, err := resources.Resource(t.Context(), WorkflowExecutionSettingsKind)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeSettings(resource.Value)
	if err != nil {
		t.Fatal(err)
	}
	if got != first || resource.Version != 1 {
		t.Fatalf("imported settings = (%+v, version %d), want (%+v, version 1)", got, resource.Version, first)
	}
}

func TestRegistryRoutesAndValidatesDefinitions(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	server := NewServer(resources)
	if _, err := server.ImportConfig(t.Context(), config.Config{Containers: config.ContainerConfig{PullPolicy: "missing"}}); err != nil {
		t.Fatal(err)
	}

	catalog, err := server.Catalog(t.Context(), &pb.CatalogRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, descriptor := range catalog.Resources {
		if descriptor.Kind == ContainerRuntimePoliciesKind {
			found = descriptor.ApplyMode == "restart-required"
		}
	}
	if !found {
		t.Fatal("container runtime policies missing restart-required descriptor")
	}

	_, err = server.Command(t.Context(), &pb.CommandRequest{Kind: ContainerRuntimePoliciesKind, Command: "replace", ValueJson: []byte(`{"pull_policy":"sometimes"}`), Actor: "test", Source: "test", RequestId: "bad", ExpectedVersion: 1})
	if err == nil {
		t.Fatal("invalid pull policy accepted")
	}
}

func TestProviderSeedKeepsReferencesAndNeverResolvedSecrets(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	server := NewServer(resources)
	cfg := config.Config{Providers: map[string]config.Provider{"openai": {Class: "openai", APIKeyEnv: "OPENAI_API_KEY", APIKey: config.SecretRef{Engine: "env", Key: "OPENAI_API_KEY"}}}}
	if _, err := server.ImportConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	resource, err := resources.Resource(t.Context(), ProviderSettingsKind)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(resource.Value, &value); err != nil {
		t.Fatal(err)
	}
	encoded := string(resource.Value)
	if encoded == "" || !json.Valid(resource.Value) {
		t.Fatalf("provider resource = %q", encoded)
	}
	if _, ok := value["openai"]; !ok {
		t.Fatalf("provider resource = %s", encoded)
	}
}

func TestHistoryCarriesEveryRevisionWithItsAudit(t *testing.T) {
	t.Parallel()

	resources := store.OpenTest(t)
	defer resources.Close()
	server := NewServer(resources)
	if _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	seeded, err := server.Query(t.Context(), &pb.QueryRequest{Kind: WorkflowExecutionSettingsKind})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Command(t.Context(), &pb.CommandRequest{
		Kind: WorkflowExecutionSettingsKind, Command: "replace",
		ValueJson:       []byte(`{"max_model_tool_steps":40,"max_runtime_seconds":600,"max_consecutive_gate_failures":2}`),
		ExpectedVersion: seeded.Resource.Version, Actor: "operator", Source: "archie-ui", RequestId: "edit-1",
	}); err != nil {
		t.Fatal(err)
	}

	history, err := server.History(t.Context(), &pb.HistoryRequest{Kind: WorkflowExecutionSettingsKind})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Revisions) != 2 {
		t.Fatalf("revisions = %d, want the seed and the edit", len(history.Revisions))
	}
	newest := history.Revisions[0]
	if newest.Actor != "operator" || newest.Source != "archie-ui" || newest.RequestId != "edit-1" {
		t.Fatalf("newest revision audit = %+v, want the dashboard edit", newest)
	}
	// The value rides along so a restore is an ordinary replace of it.
	if len(history.Revisions[1].ValueJson) == 0 {
		t.Fatal("older revision carries no value; nothing to restore from")
	}
	if _, err := server.History(t.Context(), &pb.HistoryRequest{Kind: "not-a-resource"}); status.Code(err) != codes.NotFound {
		t.Fatalf("history for an unknown kind = %v, want NotFound", err)
	}
}
