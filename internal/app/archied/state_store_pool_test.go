package archied

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
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
