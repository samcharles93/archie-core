package taskactions

import (
	"os"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }
