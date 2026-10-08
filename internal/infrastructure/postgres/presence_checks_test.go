package postgres_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

func TestPresenceReplacesDependencyChecks(t *testing.T) {
	store := postgres.New(pgstore.Pool(t))
	record := storecontract.Presence{Service: "gateway", InstanceID: "instance", StartedAt: time.Now(), ReportedAt: time.Now()}
	for _, checks := range [][]health.Component{
		{{Name: "nats", Status: health.StatusDegraded, Detail: "disconnected"}},
		{{Name: "nats", Status: health.StatusOK, Ready: true}, {Name: "state_store", Status: health.StatusOK, Ready: true}},
		{},
	} {
		record.Checks = checks
		if err := store.PutPresence(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		rows, err := store.ListPresence(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || !reflect.DeepEqual(rows[0].Checks, checks) {
			t.Fatalf("presence = %+v; want replacement checks %+v", rows, checks)
		}
	}
}
