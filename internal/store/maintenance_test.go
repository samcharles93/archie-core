package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Ownership is what makes "the State Store is stopped" a fact an offline
// command can check, so the primitive itself is asserted here rather than only
// through the commands that use it: exclusivity, release, and per-database
// independence.
func TestOwnershipIsExclusivePerDatabase(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "archie.db-tasks.sqlite")
	second := filepath.Join(dir, "other.db-tasks.sqlite")

	held, err := AcquireOwnership(first)
	if err != nil {
		t.Fatalf("first AcquireOwnership: %v", err)
	}
	if _, err := AcquireOwnership(first); !errors.Is(err, ErrStoreOwned) {
		t.Fatalf("second AcquireOwnership on the same database = %v, want ErrStoreOwned", err)
	}
	// A different database is a different claim: one process owning its store
	// must not stop another from owning its own.
	elsewhere, err := AcquireOwnership(second)
	if err != nil {
		t.Fatalf("AcquireOwnership on an unrelated database: %v", err)
	}
	if _, err := os.Stat(first + ".lock"); err != nil {
		t.Fatalf("ownership left no sidecar to lock: %v", err)
	}

	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	reacquired, err := AcquireOwnership(first)
	if err != nil {
		t.Fatalf("AcquireOwnership after Release: %v", err)
	}
	if err := errors.Join(reacquired.Release(), elsewhere.Release()); err != nil {
		t.Fatalf("release on cleanup: %v", err)
	}
}

// The snapshot an update takes is the state a rollback restores, and it is
// restored by the release that wrote it. Verifying the copy must therefore not
// migrate it: a snapshot silently upgraded to this binary's schema would be
// refused by the older binary it exists to serve.
func TestBackupSnapshotKeepsTheSchemaVersionItCopied(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "archie.db-tasks.sqlite")
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	st, err := Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// An older release's file: the schema version it wrote, which this binary
	// is expected to migrate when the store is next opened for serving.
	if err := setUserVersion(t.Context(), database, taskSchemaVersion-1); err != nil {
		t.Fatal(err)
	}

	if err := Backup(t.Context(), database, snapshot); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	version, err := ValidateFile(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("ValidateFile on the snapshot: %v", err)
	}
	if version != taskSchemaVersion-1 {
		t.Fatalf("snapshot schema version = %d, want the %d it copied", version, taskSchemaVersion-1)
	}
}

func setUserVersion(ctx context.Context, path string, version int) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version))
	return err
}
