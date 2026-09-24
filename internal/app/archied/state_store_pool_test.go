package archied

import (
	"errors"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// TestOpenStateStorePoolFailsClosedWithoutURL pins D1.2: the State Store is
// Postgres-only, so a missing database_url must stop it from starting rather
// than fall back to SQLite.
func TestOpenStateStorePoolFailsClosedWithoutURL(t *testing.T) {
	b := newBootstrap()
	b.cfg = config.Config{}
	if err := b.openStateStorePool(t.Context()); err == nil {
		t.Fatal("openStateStorePool without database_url succeeded; want fail closed")
	} else if !strings.Contains(err.Error(), "database_url is required") {
		t.Fatalf("openStateStorePool error = %v, want it to name the missing database_url", err)
	}
}

// TestOpenStateStorePoolFailsClosedOnInvalidURL pins the invalid half of D1.2:
// a URL that will not connect (here, one that does not parse) is the same
// failure as an absent one, surfaced at boot rather than at first query.
func TestOpenStateStorePoolFailsClosedOnInvalidURL(t *testing.T) {
	b := newBootstrap()
	b.cfg = config.Config{DatabaseURL: "://not-a-url"}
	if err := b.openStateStorePool(t.Context()); err == nil {
		t.Fatal("openStateStorePool with an unparseable database_url succeeded; want fail closed")
	}
}

// TestOpenStateStorePoolRefusesASecondOwner pins the serve-ownership claim: a
// second State Store against the same database fails to start while the first
// holds it, and starts once the first has shut down.
func TestOpenStateStorePoolRefusesASecondOwner(t *testing.T) {
	url := pgtest.URL(t)
	first := newBootstrap()
	first.cfg = config.Config{DatabaseURL: url}
	if err := first.openStateStorePool(t.Context()); err != nil {
		t.Fatalf("first openStateStorePool: %v", err)
	}
	second := newBootstrap()
	second.cfg = config.Config{DatabaseURL: url}
	err := second.openStateStorePool(t.Context())
	second.cleanup()
	first.cleanup()
	if !errors.Is(err, postgres.ErrOwned) {
		t.Fatalf("second openStateStorePool = %v, want ErrOwned", err)
	}
	third := newBootstrap()
	third.cfg = config.Config{DatabaseURL: url}
	if err := third.openStateStorePool(t.Context()); err != nil {
		t.Fatalf("openStateStorePool after the owner shut down: %v", err)
	}
	third.cleanup()
}
