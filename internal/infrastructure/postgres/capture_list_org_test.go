package postgres_test

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// ListCaptures returns only the acting org's captures; a caller with no org
// in context keeps the default org's.
func TestListCapturesIsOrgScoped(t *testing.T) {
	pool := pgstore.Pool(t)
	eda := postgres.NewEDA(pool, nil)
	ctx := t.Context()

	insert := func(source string) string {
		id, err := eda.InsertCapture(ctx, storecontract.CapturedEvent{Source: source, Body: "{}"}, time.Hour, 100)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sysID, acmeID := insert("sys-hook"), insert("acme-hook")
	// Captures take their org from the source, which current code always
	// writes to the default org, so reassign the fixture directly.
	if _, err := pool.Exec(ctx, "UPDATE captures SET org_id=$1 WHERE id=$2", "acme", acmeID); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		orgID   org.OrgID
		withOrg bool
		want    string
	}{
		{"no org keeps the default org", "", false, sysID},
		{"default org lists only its own", org.DefaultOrgID, true, sysID},
		{"other org lists only its own", "acme", true, acmeID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listCtx := ctx
			if tc.withOrg {
				listCtx = org.WithOrg(ctx, tc.orgID)
			}
			got, err := eda.ListCaptures(listCtx, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != tc.want {
				t.Fatalf("captures = %#v, want only %s", got, tc.want)
			}
		})
	}
}
