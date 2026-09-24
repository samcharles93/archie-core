package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// testSteps builds the production workflow step vocabulary for tests that are
// not about the vocabulary itself; step_vocabulary_agreement_test.go is the one
// that registers a provider and drives every production resolution site.
func testSteps(t *testing.T) *workflow.Manager {
	t.Helper()
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatalf("build workflow step vocabulary: %v", err)
	}
	return steps
}

// testServer builds the State Store's control plane against the production
// vocabulary, so a test that is not about the vocabulary still exercises the
// constructor production uses.
func testServer(t *testing.T, resources ResourceStore) *Server {
	t.Helper()
	server, err := NewServer(resources, testSteps(t))
	if err != nil {
		t.Fatalf("build control plane server: %v", err)
	}
	return server
}

func TestCatalogCarriesShippedWorkflowDefinitionsForRestore(t *testing.T) {
	t.Parallel()

	resources := pgstore.Open(t)
	defer resources.Close()
	catalog, err := testServer(t, resources).Catalog(t.Context(), &pb.CatalogRequest{})
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

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
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
	value, err := encodeWorkflowDefinitions(custom, testSteps(t).Registry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{Kind: WorkflowDefinitionsKind, Value: value, ExpectedVersion: resource.Version, Actor: "test", Source: "test", RequestID: "override"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
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

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
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
	if _, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{Kind: PersonasKind, Value: value, ExpectedVersion: resource.Version, Actor: "test", Source: "test", RequestID: "persona-edit"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
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

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
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

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	// The image is required, here as in the file document: a seed without one is
	// skipped rather than stored, and the Command below would then fail on the
	// missing resource instead of on the pull policy it exists to reject.
	if _, _, err := server.ImportConfig(t.Context(), config.Config{Containers: config.ContainerConfig{Image: "archie-agent:test", PullPolicy: "missing"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.Resource(t.Context(), ContainerRuntimePoliciesKind); err != nil {
		t.Fatalf("container runtime policies were not seeded: %v", err)
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

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	cfg := config.Config{Providers: map[string]config.Provider{"openai": {Class: "openai", APIKeyEnv: "OPENAI_API_KEY", APIKey: config.SecretRef{Engine: "env", Key: "OPENAI_API_KEY"}}}}
	if _, _, err := server.ImportConfig(context.Background(), cfg); err != nil {
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

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
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

// TestImportConfigSkipsASeedItCannotValidate is the reason a bad seed is not
// fatal. docs/prds/runtime-control-plane.md, "Bootstrap, migration, and
// recovery": after migration, settings in TOML are ignored and cannot block
// State Store startup -- and ImportConfig is on that startup path. The value is
// not stored either, so the setting stays file-owned: the file's value is still
// the one in effect, and editing config.toml is still the fix. Writing the
// invalid value instead would hand ownership to the database and leave the
// operator with a file edit that does nothing.
func TestImportConfigSkipsASeedItCannotValidate(t *testing.T) {
	t.Parallel()

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	// Every other field is filled in the way the loader fills it (Validate
	// documents that it does not apply defaults), so the malformed glob is the
	// only seed that can be refused.
	cfg := validConfigForValidation()
	cfg.Repos = []config.Repo{{Owner: "acme", Name: "app", TestGlob: "["}}

	versions, skipped, err := server.ImportConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("ImportConfig: %v, want a skipped seed rather than a fatal one", err)
	}
	if _, stored := versions[RepositoryPoliciesKind]; stored {
		t.Error("the resource was seeded, want the invalid seed skipped")
	}
	if len(skipped) != 1 || skipped[0].Kind != RepositoryPoliciesKind || skipped[0].Err == nil {
		t.Fatalf("skipped = %+v, want the repository policies and the reason", skipped)
	}
	if _, err := resources.Resource(t.Context(), RepositoryPoliciesKind); !errors.Is(err, storecontract.ErrResourceNotFound) {
		t.Errorf("repository policies Resource = %v, want ErrResourceNotFound: an invalid seed must not be stored", err)
	}
	// Kinds that are fine are still seeded: one skipped kind does not abandon
	// the rest of the migration.
	if _, err := resources.Resource(t.Context(), WorkflowDefinitionsKind); err != nil {
		t.Errorf("workflow definitions: %v, want them seeded alongside the skipped kind", err)
	}
}
