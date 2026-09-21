package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// This file is the offline half of the store's contract with its owner: the
// operations the State Store process cannot perform on itself, because they
// read or replace the very file it has open. They are what
// docs/prds/runtime-control-plane.md ("Bootstrap, migration, and recovery")
// asks for -- back up, restore and validate the database without archied or
// the Web UI -- and every one of them works on the file directly.
//
// The read-only operations never take the ownership lock, because they are
// what an operator runs against a serving store. The one that replaces the
// file does, because a rewrite under a running server is the only way an
// offline command can destroy a task store.

// OpenReadOnly opens an existing store for reading only: no schema is created
// and no migration runs. That matters for recovery -- the snapshot an update
// takes has to stay exactly what the release that wrote it expects, and
// migrating a copy during verification would hand a rollback a schema its
// binary refuses.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if err := RequireDatabase(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", readonlyDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// sql.Open is lazy: the first statement is where a file that is not a
	// database, a missing WAL sidecar or an unreadable path surfaces.
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&tables); err != nil {
		return nil, errors.Join(fmt.Errorf("read task database %s: %w", path, err), db.Close())
	}
	return &Store{db: db}, nil
}

// readonlyDSN reads the store without the write-ahead log pragma the serving
// process sets: a reader has no business changing the database's journal mode,
// and the read-only flag makes an accidental write an error rather than a
// silent modification of a file someone else owns.
func readonlyDSN(path string) string {
	separator := "?"
	if strings.HasSuffix(path, "?") || strings.HasSuffix(path, "&") {
		separator = ""
	} else if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "mode=ro&_pragma=busy_timeout(5000)"
}

// StoresResources reports whether the store's file carries the control-plane
// resources table. A store written before the control plane existed has none
// and holds no stored settings to validate; the serving process creates the
// table on its next start, so an offline check must tell that apart from
// resources it cannot read.
func (s *Store) StoresResources(ctx context.Context) (bool, error) {
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='resources'`).Scan(&tables); err != nil {
		return false, fmt.Errorf("inspect store schema: %w", err)
	}
	return tables > 0, nil
}

// RequireDatabase reports whether path is an existing store file. A writer
// that must not create the file it fails to find asks this first: a recovery
// command pointed at a mistyped path has to refuse, not leave a fresh empty
// database where an operator expected theirs.
func RequireDatabase(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("task database %s: %w", path, err)
	}
	return nil
}

// ValidateFile checks an existing store the way the serving process meets it
// on startup and reports the schema version it carries: the file is a SQLite
// database this binary can read, it is not corrupt, and its schema is not
// newer than this binary supports.
func ValidateFile(ctx context.Context, path string) (int, error) {
	st, err := OpenReadOnly(ctx, path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()
	return st.check(ctx)
}

// check runs the file-level checks. integrity_check returns one row per
// problem, or the single row "ok"; reading the first row is therefore the
// verdict rather than a sample.
func (s *Store) check(ctx context.Context) (int, error) {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return 0, fmt.Errorf("integrity check: %w", err)
	}
	if result != "ok" {
		return 0, fmt.Errorf("integrity check: %s", result)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if version > taskSchemaVersion {
		return 0, fmt.Errorf("database schema version %d is newer than supported version %d", version, taskSchemaVersion)
	}
	return version, nil
}

// Backup writes a transactionally consistent snapshot of database to snapshot,
// using SQLite's own copy, so a store that is being written to cannot produce
// a torn file.
//
// It deliberately does not take the ownership lock. The update installer runs
// inside the process it is updating and cannot stop the State Store, so the
// snapshot that makes a failed update reversible is taken against a serving
// store; a backup that insisted on an exclusively owned file would break the
// one caller that exists. The snapshot is written beside the destination and
// renamed into place, so an interrupted backup never replaces a snapshot that
// was good with one that is not.
func Backup(ctx context.Context, database, snapshot string) error {
	if err := RequireDatabase(database); err != nil {
		return err
	}
	// The exemption from the ownership lock below is only sound while backup
	// never replaces the database. With -out naming the database it would
	// rename the snapshot over a file a serving State Store may still have
	// open, orphaning the WAL of the file it just unlinked -- so it is refused
	// exactly as Restore refuses it.
	if same, err := sameFile(database, snapshot); err != nil {
		return err
	} else if same {
		return fmt.Errorf("snapshot %s is the database itself", snapshot)
	}
	if err := os.MkdirAll(filepath.Dir(snapshot), 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(database))
	if err != nil {
		return fmt.Errorf("open task database %s: %w", database, err)
	}
	defer func() { _ = db.Close() }()

	staging := snapshot + ".tmp"
	if err := os.Remove(staging); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear staging snapshot %s: %w", staging, err)
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, staging); err != nil {
		return errors.Join(fmt.Errorf("snapshot %s: %w", database, err), os.Remove(staging))
	}
	// A snapshot that is not itself a usable store is worse than no snapshot:
	// the update path treats its existence as proof that a rollback is
	// possible.
	if _, err := ValidateFile(ctx, staging); err != nil {
		return errors.Join(fmt.Errorf("snapshot is not a usable store: %w", err), os.Remove(staging))
	}
	if err := os.Rename(staging, snapshot); err != nil {
		return errors.Join(fmt.Errorf("publish snapshot %s: %w", snapshot, err), os.Remove(staging))
	}
	return nil
}

// Restore replaces database with the snapshot at snapshot. The snapshot is
// verified before anything is destroyed, so a file that is not a usable store
// cannot take the database down with it, and the database file itself is
// replaced by a rename, so a failure leaves either the old file or the new one
// rather than a mixture. The replaced file's -wal/-shm are removed first, which
// the comment at that step explains: between the two calls the database is
// merely incomplete, and re-running the command completes it.
//
// It holds the ownership lock for its duration: rewriting a database a running
// State Store still has open would leave that process writing to an unlinked
// inode while the store served a file nobody owns.
func Restore(ctx context.Context, database, snapshot string) error {
	if _, err := os.Stat(filepath.Dir(database)); err != nil {
		return fmt.Errorf("restore destination %s: %w", database, err)
	}
	if same, err := sameFile(database, snapshot); err != nil {
		return err
	} else if same {
		return fmt.Errorf("restore snapshot %s is the database itself", snapshot)
	}
	ownership, err := AcquireOwnership(database)
	if err != nil {
		return err
	}
	defer func() { _ = ownership.Release() }()

	if _, err := ValidateFile(ctx, snapshot); err != nil {
		return fmt.Errorf("snapshot is not a usable store: %w", err)
	}
	staging := database + ".restore.tmp"
	if err := copyFile(snapshot, staging); err != nil {
		return errors.Join(err, os.Remove(staging))
	} // The replaced database's write-ahead log belongs to the file that is
	// going away. It goes first: between the two calls the database is merely
	// incomplete, whereas a WAL left beside the file it does not describe is
	// the state SQLite cannot recover from.
	for _, sidecar := range []string{"-wal", "-shm"} {
		if err := os.Remove(database + sidecar); err != nil && !os.IsNotExist(err) {
			return errors.Join(fmt.Errorf("remove %s%s: %w", database, sidecar, err), os.Remove(staging))
		}
	}
	if err := os.Rename(staging, database); err != nil {
		return errors.Join(fmt.Errorf("publish restored database %s: %w", database, err), os.Remove(staging))
	}
	return nil
}

func sameFile(a, b string) (bool, error) {
	first, err := os.Stat(a)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	second, err := os.Stat(b)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(first, second), nil
}

// copyFile writes source over a new file at path, flushed to disk before it is
// returned: the rename that publishes it must not reach the directory entries
// ahead of the bytes they point at. The snapshot's own permissions are kept, so
// a restore does not silently change who may read the operator's database.
func copyFile(source, path string) (retErr error) {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat snapshot %s: %w", source, err)
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open snapshot %s: %w", source, err)
	}
	defer func() { retErr = errors.Join(retErr, in.Close()) }()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { retErr = errors.Join(retErr, out.Close()) }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return nil
}
