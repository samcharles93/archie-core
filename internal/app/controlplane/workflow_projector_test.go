package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// testPackageProjector builds the workflow package projector against a real
// migrated database and the production step vocabulary. The projector's
// ledger rows describe installed packages, so a fixture installs one.
func testPackageProjector(ctx context.Context, t *testing.T, orgID, pkgName, digest string) (*WorkflowProjector, ResourceStore, storepkg.ProjectionLedger) {
	t.Helper()
	db := pgstore.Open(t)
	installed := storepkg.Installed{
		OrgID: orgID, Name: pkgName, Reference: "localhost:5000/" + pkgName,
		Digest: digest, Descriptor: testPackageDescriptor(), Layer: []byte("layer"), UpdatePolicy: "manual",
	}
	if err := postgres.NewInstalledPackages(db.Pool).Install(ctx, installed); err != nil {
		t.Fatalf("install package record: %v", err)
	}
	resources := postgres.NewResources(db.Pool)
	ledger := postgres.NewPackageContributions(db.Pool)
	projector, err := NewWorkflowPackageProjector(resources, ledger, testSteps(t))
	if err != nil {
		t.Fatalf("build workflow projector: %v", err)
	}
	return projector, resources, ledger
}

// testPackageDescriptor is the valid descriptor backing the ledger fixture.
func testPackageDescriptor() storepkg.Descriptor {
	return storepkg.Descriptor{
		APIVersion: storepkg.APIVersion, DisplayName: "Review pack", Version: "1",
		Contributes: storepkg.Contributions{Workflows: []string{"workflows/review.yaml"}},
	}
}

// contributedWorkflow builds a contributed workflow the shipped vocabulary
// compiles, with a fresh id no shipped or operator workflow claims.
func contributedWorkflow(name string) workflow.WorkflowDefinitionEntry {
	return workflow.WorkflowDefinitionEntry{ID: name, YAML: "id: " + name + "\nsteps:\n  - type: bootstrap.apply\n"}
}

// storedCollection returns an org's workflow-definitions document the way its
// readers see it: shipped definitions stand in for an absent row.
func storedCollection(ctx context.Context, t *testing.T, resources ResourceStore, orgID string) (workflow.WorkflowDefinitionCollection, int64) {
	t.Helper()
	resource, err := resources.Resource(ctx, orgID, WorkflowDefinitionsKind)
	if errors.Is(err, storecontract.ErrResourceNotFound) {
		return workflow.ShippedDefinitions(), 0
	}
	if err != nil {
		t.Fatal(err)
	}
	collection, err := workflow.DecodeDefinitionCollection(resource.Value, workflow.BuiltinStepRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return collection, resource.Version
}

// operatorEdit installs a workflow the operator owns, exactly a dashboard
// replacement does.
func operatorEdit(ctx context.Context, t *testing.T, resources ResourceStore, orgID string, entry workflow.WorkflowDefinitionEntry) {
	t.Helper()
	collection, version := storedCollection(ctx, t, resources, orgID)
	collection.Definitions = append(collection.Definitions, entry)
	value, err := encodeWorkflowDefinitions(collection, testSteps(t).Registry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.PutResource(ctx, storecontract.ResourceWrite{
		OrgID: orgID, Kind: WorkflowDefinitionsKind, Value: value, ExpectedVersion: version,
		Actor: "operator", Source: "dashboard", RequestID: "operator-edit",
	}); err != nil {
		t.Fatal(err)
	}
}

func resourceVersionOf(ctx context.Context, t *testing.T, resources ResourceStore, orgID string) int64 {
	t.Helper()
	_, version := storedCollection(ctx, t, resources, orgID)
	return version
}

func TestWorkflowProjectorApplyAndWithdraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const pkgName = "review"
	const contributedID = "review"
	contributedYAML := contributedWorkflow(contributedID).YAML

	// applied builds a projector that has projected one contributed workflow.
	applied := func(t *testing.T) (*WorkflowProjector, ResourceStore, storepkg.ProjectionLedger) {
		t.Helper()
		projector, resources, ledger := testPackageProjector(ctx, t, "org-a", pkgName, testDigest)
		if err := projector.Apply(ctx, "org-a", pkgName, testDigest, map[string][]byte{
			"workflows/review.yaml": []byte(contributedYAML),
		}); err != nil {
			t.Fatal(err)
		}
		return projector, resources, ledger
	}

	t.Run("apply makes the contributed workflow an org workflow without a restart", func(t *testing.T) {
		_, resources, ledger := applied(t)
		collection, _ := storedCollection(ctx, t, resources, "org-a")
		if _, ok := collection.DefinitionByID(contributedID); !ok {
			t.Fatal("org workflow-definitions does not carry the contributed workflow")
		}
		entries, err := ledger.Entries(ctx, "org-a", pkgName)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0] != (storepkg.ProjectionEntry{Family: storepkg.FamilyWorkflows, EntryID: contributedID}) {
			t.Fatalf("ledger = %#v", entries)
		}
	})
	t.Run("withdraw takes exactly the package's entries back out", func(t *testing.T) {
		projector, resources, _ := testPackageProjector(ctx, t, "org-a", pkgName, testDigest)
		other := contributedWorkflow("review-operator")
		if err := projector.Apply(ctx, "org-a", pkgName, testDigest, map[string][]byte{
			"workflows/review.yaml": []byte(contributedYAML),
		}); err != nil {
			t.Fatal(err)
		}
		operatorEdit(ctx, t, resources, "org-a", other)
		if err := projector.Withdraw(ctx, "org-a", pkgName, testDigest); err != nil {
			t.Fatal(err)
		}
		collection, _ := storedCollection(ctx, t, resources, "org-a")
		if _, ok := collection.DefinitionByID(contributedID); ok {
			t.Fatal("the package's contributed workflow survives withdrawal")
		}
		if _, ok := collection.DefinitionByID(other.ID); !ok {
			t.Fatal("operator workflow was withdrawn by a package remove")
		}
	})
	t.Run("a contributed id colliding with an operator workflow is refused", func(t *testing.T) {
		projector, resources, _ := testPackageProjector(ctx, t, "org-a", pkgName, testDigest)
		operatorEdit(ctx, t, resources, "org-a", contributedWorkflow(contributedID))
		before := resourceVersionOf(ctx, t, resources, "org-a")
		if err := projector.Apply(ctx, "org-a", pkgName, testDigest, map[string][]byte{
			"workflows/review.yaml": []byte(contributedYAML),
		}); !errors.Is(err, storepkg.ErrContributionCollision) {
			t.Fatalf("colliding contribution error = %v", err)
		}
		if after := resourceVersionOf(ctx, t, resources, "org-a"); after != before {
			t.Fatal("refused contribution wrote the resource anyway")
		}
	})
	t.Run("an invalid contributed workflow is refused without a write", func(t *testing.T) {
		projector, resources, _ := testPackageProjector(ctx, t, "org-a", pkgName, testDigest)
		before := resourceVersionOf(ctx, t, resources, "org-a")
		if err := projector.Apply(ctx, "org-a", pkgName, testDigest, map[string][]byte{
			"workflows/broken.yaml": []byte("id: broken\nsteps:\n  - type: not-a-step\n"),
		}); err == nil {
			t.Fatal("invalid workflow definition projected")
		}
		if after := resourceVersionOf(ctx, t, resources, "org-a"); after != before {
			t.Fatal("refused projection wrote the resource anyway")
		}
	})
	t.Run("withdraw of a package that never applied is a no-op", func(t *testing.T) {
		projector, _, _ := testPackageProjector(ctx, t, "org-x", pkgName, testDigest)
		if err := projector.Withdraw(ctx, "org-x", pkgName, testDigest); err != nil {
			t.Fatal(err)
		}
	})
}
