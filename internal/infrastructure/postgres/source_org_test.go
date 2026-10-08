package postgres_test

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// TestInsertSourceKeepsActingOrg pins that a source created while acting in a
// non-default org is owned by that org, so its captures inherit the org.
func TestInsertSourceKeepsActingOrg(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	eda := postgres.NewEDA(db.Pool, nil)
	if err := eda.InsertSource(org.WithOrg(ctx, "acme"), source.Source{Path: "acme-hook", Signing: source.SigningUnsigned}); err != nil {
		t.Fatal(err)
	}
	var owner, workspace string
	if err := db.Pool.QueryRow(ctx, "SELECT org_id, workspace_id FROM sources WHERE path=$1", "acme-hook").Scan(&owner, &workspace); err != nil {
		t.Fatal(err)
	}
	if owner != "acme" || workspace != string(org.DefaultWorkspaceID) {
		t.Fatalf("source owner %s/%s, want acme/%s", owner, workspace, org.DefaultWorkspaceID)
	}
	// Lookup stays global: the capture receiver resolves by path alone.
	got, err := eda.GetSource(ctx, "acme-hook")
	if err != nil || got == nil {
		t.Fatalf("GetSource = %#v, err %v", got, err)
	}
}
