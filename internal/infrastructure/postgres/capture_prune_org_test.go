package postgres_test

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// Count retention keeps the newest N captures within the just-inserted
// capture's org, so one org's volume never evicts another org's captures.
func TestCaptureCountPruneStaysInOrg(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	eda := postgres.NewEDA(db.Pool, nil)
	for _, owned := range []struct{ path, org string }{{"prune-a", "org-a"}, {"prune-b", "org-b"}} {
		if err := eda.InsertSource(ctx, source.Source{Path: owned.path, Signing: source.SigningUnsigned}); err != nil {
			t.Fatal(err)
		}
		// Source ownership is set by another seam; write it directly.
		if _, err := db.Pool.Exec(ctx, "UPDATE sources SET org_id=$1 WHERE path=$2", owned.org, owned.path); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC().Truncate(time.Second)
	insert := func(path string, at time.Time) string {
		id, err := eda.InsertCapture(ctx, storecontract.CapturedEvent{Source: path, Body: "{}", ReceivedAt: at}, 0, 3)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Quiet org first: its captures must survive the loud org's volume.
	for i := range 2 {
		insert("prune-b", base.Add(time.Duration(i)*time.Second))
	}
	var loud []string
	for i := range 5 {
		loud = append(loud, insert("prune-a", base.Add(time.Duration(10+i)*time.Second)))
	}
	count := func(owner string) int {
		var n int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM captures WHERE org_id=$1", owner).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	present := func(id string) bool {
		var n int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM captures WHERE id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if got := count("org-b"); got != 2 {
		t.Fatalf("quiet org kept %d captures, want 2", got)
	}
	if got := count("org-a"); got != 3 {
		t.Fatalf("loud org kept %d captures, want 3", got)
	}
	for _, id := range loud[:2] {
		if present(id) {
			t.Fatalf("loud org's oldest capture %s survived past the cap", id)
		}
	}
	for _, id := range loud[2:] {
		if !present(id) {
			t.Fatalf("loud org's newest capture %s missing", id)
		}
	}
}
