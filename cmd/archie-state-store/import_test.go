package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyimport/legacyfixture"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// TestRecoveryImportMovesTheInstallOnce drives the import the way an operator
// does: the configured db_path locates every legacy file, and a second run
// against the same database is refused because the first one completed.
func TestRecoveryImportMovesTheInstallOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "archie.db")
	legacyfixture.Install(t, dbPath)
	url := pgtest.URL(t)

	code, stdout, stderr := runRecoveryCmd(t, "import", "-db-path", dbPath, "-database-url", url)
	if code != 0 {
		t.Fatalf("import exited %d; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "tasks=2") {
		t.Errorf("import summary %q does not report the imported task", stdout)
	}

	pool, err := postgres.Open(t.Context(), url)
	if err != nil {
		t.Fatalf("postgres.Open: %v", err)
	}
	defer pool.Close()
	var issue int64
	if err := pool.QueryRow(t.Context(), "SELECT issue_number FROM tasks WHERE id = 1").Scan(&issue); err != nil || issue != 7 {
		t.Fatalf("imported task 1 issue = %d, %v; want 7", issue, err)
	}

	code, _, stderr = runRecoveryCmd(t, "import", "-db-path", dbPath, "-database-url", url)
	if code != 1 || !strings.Contains(stderr, "already holds a completed import") {
		t.Fatalf("second import exited %d, stderr %q; want 1 naming the completed import", code, stderr)
	}
}
