package archied

import (
	"os"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// TestMain runs the archied test binary against a throwaway PostgreSQL 18
// container, mirroring internal/infrastructure/postgres. The State Store's
// openStateStore fails closed without a working database_url, so the tests
// that exercise it (e.g. TestStateStoreDataSurvivesRestart) need a real pool.
func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }
