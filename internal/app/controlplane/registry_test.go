package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// retiredStepType names a step type no registry declares. A stored collection
// carrying one is what a database seeded by an earlier release holds once a
// built-in workflow's step vocabulary changes under it.
const retiredStepType = "legacy.retired-step"

// staleWorkflowDefinitions is a stored workflow-definitions value this build
// refuses. ValidateDefinitionCollection validates the collection as one
// document, which is what makes one stale entry fail every task's pin, whatever
// workflow that task names.
//
// It is encoded directly rather than through encodeWorkflowDefinitions, because
// that runs the validator a write applies: the point of the fixture is the
// document an earlier release wrote, which this build's write path would refuse
// (the validator cannot bless the value whose rejection is the bug reported).
func staleWorkflowDefinitions(t *testing.T) []byte {
	t.Helper()
	value, err := json.Marshal(workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
		{ID: "legacy", YAML: "id: legacy\nsteps:\n  - type: " + retiredStepType + "\n"},
	}})
	if err != nil {
		t.Fatalf("encode the stale definitions: %v", err)
	}
	if _, err := workflow.DecodeDefinitionCollection(value, testSteps(t).Registry()); err == nil {
		t.Fatal("the stored value under test decodes; it is not the stale state this test is about")
	}
	return value
}

// writeSeeded writes value as the revision an earlier release's seed left
// behind: the seed's own audit identity, and the seed's own request ID for that
// value. The producer supplies both -- the value is the only thing substituted
// -- so a test does not restate a key or an identity it would then be pinning.
func writeSeeded(t *testing.T, resources *pgstore.TaskDB, kind string, value []byte) storecontract.Resource {
	t.Helper()
	if current, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, kind); err == nil {
		t.Fatalf("%s already holds version %d; a seeded fixture writes the first revision", kind, current.Version)
	} else if !errors.Is(err, storecontract.ErrResourceNotFound) {
		t.Fatalf("read %s: %v", kind, err)
	}
	seeded, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		OrgID: storecontract.DefaultOrgID,
		Kind:  kind, Value: value, ExpectedVersion: 0,
		Actor: seedActor, Source: seedSource, RequestID: seedRequestID(kind, value),
	})
	if err != nil {
		t.Fatalf("write the seeded %s revision: %v", kind, err)
	}
	return seeded
}

// replaceAsOperator replaces a kind the way the dashboard does: a write carrying
// the operator's attribution, which is what marks the value as theirs.
func replaceAsOperator(t *testing.T, resources *pgstore.TaskDB, kind string, value []byte, expected int64) storecontract.Resource {
	t.Helper()
	replaced, err := resources.PutResource(t.Context(), storecontract.ResourceWrite{
		OrgID: storecontract.DefaultOrgID,
		Kind:  kind, Value: value, ExpectedVersion: expected,
		Actor: "operator", Source: "archie-ui", RequestID: "operator-replace-" + kind,
	})
	if err != nil {
		t.Fatalf("replace %s: %v", kind, err)
	}
	return replaced
}

// TestImportConfigRefreshesAStaleShippedValue is defect 1 of #1233. A seed was
// consulted only for a kind the store did not hold, so a document that changed
// in the build never reached a database an earlier release seeded -- and for
// workflow-definitions that is not a stale setting but an outage, because the
// collection validates as one document and a step type this build no longer has
// fails every task's pin.
func TestImportConfigRefreshesAStaleShippedValue(t *testing.T) {
	t.Parallel()

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	// The state under test is "the seed's own document, one release old" -- the
	// state the issue reports -- rather than "a value somebody else wrote",
	// which is the distinction the refresh turns on.
	seeded := writeSeeded(t, resources, WorkflowDefinitionsKind, staleWorkflowDefinitions(t))

	versions, _, err := server.ImportConfig(t.Context(), config.Config{})
	if err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}
	refreshed, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Version <= seeded.Version {
		t.Fatalf("%s is still at version %d: the stale seed value was not replaced", WorkflowDefinitionsKind, refreshed.Version)
	}
	definitions, err := workflow.DecodeDefinitionCollection(refreshed.Value, testSteps(t).Registry())
	if err != nil {
		t.Fatalf("the stored workflow definitions still do not decode, so no task can pin: %v", err)
	}
	if len(definitions.Definitions) != len(workflow.ShippedDefinitions().Definitions) {
		t.Fatalf("stored definitions = %d, want the shipped %d", len(definitions.Definitions), len(workflow.ShippedDefinitions().Definitions))
	}
	if versions[WorkflowDefinitionsKind] != refreshed.Version {
		t.Fatalf("the import reported version %d, want the version the store holds, %d", versions[WorkflowDefinitionsKind], refreshed.Version)
	}
	// A refresh is a write like any other: what it replaced stays a revision, so
	// the value the previous release shipped is still readable.
	history, err := resources.ResourceHistory(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || !bytes.Equal(history[1].Value, staleWorkflowDefinitions(t)) {
		t.Fatalf("history = %+v, want the replaced value kept as a revision", history)
	}
}

// TestImportConfigLeavesASeededValueAloneOnceItMatches pins the other half of the
// refresh: a boot that finds the value it would write writes nothing. A seed that
// re-wrote its own document on every start would add a revision per boot, and the
// revision an operator reads as "what I am running" would move for no reason.
func TestImportConfigLeavesASeededValueAloneOnceItMatches(t *testing.T) {
	t.Parallel()

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	for attempt := 1; attempt <= 2; attempt++ {
		if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
			t.Fatalf("ImportConfig %d: %v", attempt, err)
		}
		stored, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Version != 1 {
			t.Fatalf("after %d seed(s) %s is at version %d, want the one revision", attempt, WorkflowDefinitionsKind, stored.Version)
		}
	}
}

// TestImportConfigKeepsAStoredValueThatIsNotTheSeeds pins the half the refresh
// must not break. The seed writes a value it produced itself; a value it did not
// produce is somebody's, and two kinds of somebody are covered here:
//
//   - an operator's replacement, which the seed must leave alone on the boot
//     that finds it and on every boot after -- a refresh that consulted the
//     store's copy rather than the ledger would take the value back the moment
//     it had written it once;
//   - a kind derived from the file config, where the file is only a seed by
//     design, so a changed config.toml does not reach a database that holds a
//     value for it.
func TestImportConfigKeepsAStoredValueThatIsNotTheSeeds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind string
		// settle leaves the store holding the value the imports must not touch,
		// and returns that value.
		settle func(t *testing.T, resources *pgstore.TaskDB) []byte
		// cfg is the file config the imports run with. For the file-derived
		// case it names a different value from the one that was seeded.
		cfg func() config.Config
		// verify checks the stored value after the imports, given the value
		// settle left behind.
		verify func(t *testing.T, got, want []byte)
	}{
		{
			name: "an operator's replacement, whose shipped document also moved on",
			kind: WorkflowDefinitionsKind,
			cfg:  func() config.Config { return config.Config{} },
			settle: func(t *testing.T, resources *pgstore.TaskDB) []byte {
				t.Helper()
				// The seed wrote a document this build refuses, and the operator
				// then replaced it. The stored value is therefore neither the
				// seed's nor the one this build ships, which is exactly the
				// state a refresh keyed on authorship has to leave alone.
				seeded := writeSeeded(t, resources, WorkflowDefinitionsKind, staleWorkflowDefinitions(t))
				value, err := encodeWorkflowDefinitions(workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
					{ID: "operator-only", YAML: "id: operator-only\nsteps:\n  - type: bootstrap.apply\n"},
				}}, testSteps(t).Registry())
				if err != nil {
					t.Fatal(err)
				}
				return replaceAsOperator(t, resources, WorkflowDefinitionsKind, value, seeded.Version).Value
			},
			verify: func(t *testing.T, got, want []byte) {
				t.Helper()
				definitions, err := workflow.DecodeDefinitionCollection(got, testSteps(t).Registry())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) || len(definitions.Definitions) != 1 || definitions.Definitions[0].ID != "operator-only" {
					t.Fatalf("stored value = %s, want the operator's replacement kept", got)
				}
			},
		},
		{
			name: "a value derived from the file config, whose config.toml changed",
			kind: RepositoryPoliciesKind,
			cfg: func() config.Config {
				cfg := validConfigForValidation()
				cfg.Repos = []config.Repo{{Owner: "acme", Name: "widget"}}
				return cfg
			},
			settle: func(t *testing.T, resources *pgstore.TaskDB) []byte {
				t.Helper()
				resolved := validConfigForValidation()
				resolved.Repos = []config.Repo{{Owner: "acme", Name: "app"}}
				if _, _, err := testServer(t, resources).ImportConfig(t.Context(), resolved); err != nil {
					t.Fatal(err)
				}
				stored, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, RepositoryPoliciesKind)
				if err != nil {
					t.Fatal(err)
				}
				return stored.Value
			},
			verify: func(t *testing.T, got, want []byte) {
				t.Helper()
				if !bytes.Equal(got, want) {
					t.Fatalf("stored repository policies = %s, want the seeded %s: the file is a seed, not the owner", got, want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resources := pgstore.Open(t)
			defer resources.Close()
			server := testServer(t, resources)
			want := tt.settle(t, resources)
			before, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, tt.kind)
			if err != nil {
				t.Fatal(err)
			}

			for attempt := 1; attempt <= 2; attempt++ {
				versions, _, err := server.ImportConfig(t.Context(), tt.cfg())
				if err != nil {
					t.Fatalf("ImportConfig %d: %v", attempt, err)
				}
				after, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, tt.kind)
				if err != nil {
					t.Fatal(err)
				}
				if after.Version != before.Version {
					t.Fatalf("import %d moved %s from version %d to %d: the stored value is not the seed's to replace",
						attempt, tt.kind, before.Version, after.Version)
				}
				if versions[tt.kind] != before.Version {
					t.Fatalf("import %d reported version %d for %s, want the stored %d", attempt, versions[tt.kind], tt.kind, before.Version)
				}
				tt.verify(t, after.Value, want)
			}
		})
	}
}

// TestImportConfigRecreatesAKindAnOperatorRemoved is defect 2 of #1233: the
// documented recovery -- remove the resource, restart -- wrote nothing. The seed's
// request ID is in resource_history, that ledger outlives the row it describes,
// and a write whose request ID is already in the ledger is answered as a replay,
// so the seed reported a version for a kind that was not there and left it absent.
func TestImportConfigRecreatesAKindAnOperatorRemoved(t *testing.T) {
	t.Parallel()

	resources := pgstore.Open(t)
	defer resources.Close()
	server := testServer(t, resources)
	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	seeded, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind)
	if err != nil {
		t.Fatal(err)
	}
	history, err := resources.ResourceHistory(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("seed revisions = %d, want the one the removal leaves behind", len(history))
	}

	if _, err := resources.Pool.Exec(t.Context(), `DELETE FROM resources WHERE kind = $1`, WorkflowDefinitionsKind); err != nil {
		t.Fatalf("remove the resource: %v", err)
	}
	if _, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind); !errors.Is(err, storecontract.ErrResourceNotFound) {
		t.Fatalf("resource after the removal = %v, want ErrResourceNotFound", err)
	}

	if _, _, err := server.ImportConfig(t.Context(), config.Config{}); err != nil {
		t.Fatalf("ImportConfig after the removal: %v", err)
	}
	restored, err := resources.Resource(t.Context(), storecontract.DefaultOrgID, WorkflowDefinitionsKind)
	if err != nil {
		t.Fatalf("the removed %s was not re-seeded: %v", WorkflowDefinitionsKind, err)
	}
	if restored.Version != seeded.Version || !bytes.Equal(restored.Value, seeded.Value) {
		t.Fatalf("re-seeded %s = version %d, want the seed back at version %d", WorkflowDefinitionsKind, restored.Version, seeded.Version)
	}
	if _, err := workflow.DecodeDefinitionCollection(restored.Value, testSteps(t).Registry()); err != nil {
		t.Fatalf("the re-seeded workflow definitions do not decode: %v", err)
	}
}
