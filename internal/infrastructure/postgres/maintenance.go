package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

// The serving roles that each hold an ownership claim for their whole life.
const (
	OwnerStateStore = "state-store"
	OwnerGateway    = "gateway"
)

// ServiceOwners is every serving role that reads or writes the database. A
// destructive offline operation must hold all of them: one database backs
// every service, so the State Store being stopped alone proves nothing about
// the Gateway.
var ServiceOwners = []string{OwnerStateStore, OwnerGateway}

// ErrSchemaTooNew is returned for a database migrated by a newer release than
// this binary. goose Down is not a rollback (it drops tables), so the only way
// back is restoring the snapshot taken before that migration.
var ErrSchemaTooNew = errors.New("postgres: database schema is newer than this binary supports")

// gooseTable is goose's version table; its presence is what marks an archive
// as a dump of an archie database rather than of anything pg_restore can read.
const gooseTable = "public.goose_db_version"

// SupportedSchemaVersion is the highest migration this binary embeds.
func SupportedSchemaVersion() int64 {
	names, err := fs.Glob(Migrations(), "*.sql")
	if err != nil {
		// Unreachable: the pattern is well formed.
		panic(err)
	}
	var highest int64
	for _, name := range names {
		if version, err := goose.NumericComponent(name); err == nil {
			highest = max(highest, version)
		}
	}
	return highest
}

// CheckSchemaVersion refuses a schema this binary cannot serve.
func CheckSchemaVersion(version int64) error {
	if supported := SupportedSchemaVersion(); version > supported {
		return fmt.Errorf("%w (database at %d, binary supports %d)", ErrSchemaTooNew, version, supported)
	}
	return nil
}

// SchemaVersion reads the applied goose version without migrating. An
// unmigrated database is version 0.
func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var version int64
	err := pool.QueryRow(ctx, `SELECT CASE WHEN to_regclass('public.goose_db_version') IS NULL THEN 0
		ELSE (SELECT coalesce(max(version_id), 0) FROM public.goose_db_version WHERE is_applied) END`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("postgres: read schema version: %w", err)
	}
	return version, nil
}

// AcquireAll claims every name, or none: on any failure the claims already
// taken are released before the error returns.
func AcquireAll(ctx context.Context, pool *pgxpool.Pool, names []string) (release func(context.Context), err error) {
	held := make([]*Ownership, 0, len(names))
	release = func(ctx context.Context) {
		for _, o := range slices.Backward(held) {
			_ = o.Release(ctx)
		}
	}
	for _, name := range names {
		o, err := AcquireOwnership(ctx, pool, name)
		if err != nil {
			release(ctx)
			return nil, err
		}
		held = append(held, o)
	}
	return release, nil
}

// Backup writes a whole-database pg_dump archive to out. It takes no claim:
// pg_dump reads one consistent snapshot, so it is safe against serving
// processes, which is what the update installer needs. The archive goes to a
// scratch file and replaces out only once pg_restore can read it back, so a
// failed run never costs the previous good snapshot.
func Backup(ctx context.Context, databaseURL, out string) error {
	tmp, err := os.CreateTemp(filepath.Dir(out), filepath.Base(out)+".tmp*")
	if err != nil {
		return fmt.Errorf("postgres: backup: %w", err)
	}
	scratch := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(scratch) }()

	dbname, env := libpqTarget(databaseURL)
	if _, err := runTool(ctx, env, "pg_dump", dumpArgs(dbname, scratch)...); err != nil {
		return fmt.Errorf("postgres: backup: %w", err)
	}
	if _, err := ValidateSnapshot(ctx, scratch); err != nil {
		return fmt.Errorf("postgres: backup produced an unreadable snapshot: %w", err)
	}
	if err := os.Rename(scratch, out); err != nil {
		return fmt.Errorf("postgres: backup: %w", err)
	}
	return nil
}

// ValidateSnapshot reads the archive's table of contents and returns the
// tables it holds. An archive without goose's version table is not a dump of
// an archie database.
func ValidateSnapshot(ctx context.Context, path string) ([]string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("postgres: snapshot: %w", err)
	}
	list, err := runTool(ctx, nil, "pg_restore", "--list", path)
	if err != nil {
		return nil, fmt.Errorf("postgres: snapshot %s: %w", path, err)
	}
	tables := snapshotTables(list)
	if !slices.Contains(tables, gooseTable) {
		return nil, fmt.Errorf("postgres: snapshot %s holds no %s; it is not an archie database dump", path, gooseTable)
	}
	return tables, nil
}

// Restore replaces the whole database with the snapshot at from. It is
// destructive: every write after the snapshot is lost. It refuses unless it
// can claim every service role, so no serving process is running and none can
// start until it finishes; a claim that cannot be checked is a refusal too.
//
// pg_restore --clean drops only what the archive holds, so tables a later
// migration created are dropped first; otherwise they would survive into the
// restored schema and break the next upgrade's migration.
func Restore(ctx context.Context, databaseURL, from string) error {
	tables, err := ValidateSnapshot(ctx, from)
	if err != nil {
		return err
	}
	pool, err := Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("postgres: restore cannot confirm every service is stopped: %w", err)
	}
	defer pool.Close()
	release, err := AcquireAll(ctx, pool, ServiceOwners)
	if err != nil {
		return fmt.Errorf("postgres: restore refused, stop every archie service first: %w", err)
	}
	defer release(context.WithoutCancel(ctx))

	if err := dropTablesNotIn(ctx, pool, tables); err != nil {
		return err
	}
	dbname, env := libpqTarget(databaseURL)
	if _, err := runTool(ctx, env, "pg_restore", restoreArgs(dbname, from)...); err != nil {
		return fmt.Errorf("postgres: restore: %w", err)
	}
	return nil
}

func dropTablesNotIn(ctx context.Context, pool *pgxpool.Pool, keep []string) error {
	rows, err := pool.Query(ctx, `SELECT schemaname, tablename FROM pg_tables
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema')`)
	if err != nil {
		return fmt.Errorf("postgres: restore: list tables: %w", err)
	}
	var drop []pgx.Identifier
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: restore: list tables: %w", err)
		}
		if !slices.Contains(keep, schema+"."+table) {
			drop = append(drop, pgx.Identifier{schema, table})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: restore: list tables: %w", err)
	}
	for _, table := range drop {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table.Sanitize()+" CASCADE"); err != nil {
			return fmt.Errorf("postgres: restore: drop %s: %w", table.Sanitize(), err)
		}
	}
	return nil
}

// snapshotTables extracts schema.table for every TABLE entry of a
// `pg_restore --list` listing, sorted.
func snapshotTables(list string) []string {
	var tables []string
	for line := range strings.Lines(list) {
		if strings.HasPrefix(line, ";") {
			continue
		}
		_, entry, ok := strings.Cut(line, ";")
		if !ok {
			continue
		}
		fields := strings.Fields(entry)
		// "tableoid oid TABLE schema name owner"; "TABLE DATA" entries are rows.
		if len(fields) >= 5 && fields[2] == "TABLE" && fields[3] != "DATA" {
			tables = append(tables, fields[3]+"."+fields[4])
		}
	}
	slices.Sort(tables)
	return tables
}

func dumpArgs(dbname, out string) []string {
	return []string{"--format=custom", "--no-password", "--file=" + out, "--dbname=" + dbname}
}

func restoreArgs(dbname, from string) []string {
	return []string{"--clean", "--if-exists", "--single-transaction", "--exit-on-error", "--no-owner", "--no-password", "--dbname=" + dbname, from}
}

// libpqTarget splits a connection URL into the dbname argument and the
// environment the client tools read, moving the password to PGPASSWORD so it
// never appears in a process listing. A key=value connection string is passed
// through as-is.
func libpqTarget(databaseURL string) (string, []string) {
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil {
		return databaseURL, nil
	}
	password, ok := u.User.Password()
	if !ok {
		return databaseURL, nil
	}
	u.User = url.User(u.User.Username())
	return u.String(), []string{"PGPASSWORD=" + password}
}

// runTool runs a PostgreSQL client tool and returns its stdout; a failure
// carries its stderr, which is where the tools explain themselves.
func runTool(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
