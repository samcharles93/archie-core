package postgres_test

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
)

// orgSysTables are the tables whose org_id defaulted to 'default' before the
// rename; the migration points them at 'org-sys'.
var orgSysTables = []string{
	"bindings", "captures", "event_types", "events", "mappings",
	"resource_history", "resources", "sources", "step_executions", "tasks",
	"tool_calls", "transitions",
}

// TestOrgSysMigrationMovesExistingDefaultRows exercises the rename on the path
// a real install takes: a database at the previous schema with rows already
// under the old org id.
func TestOrgSysMigrationMovesExistingDefaultRows(t *testing.T) {
	ctx := t.Context()
	pool, err := postgres.Open(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, postgres.Migrations())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := provider.UpTo(ctx, 32); err != nil {
		t.Fatalf("migrate to 32: %v", err)
	}
	seedDefaultOrg(t, pool)
	if _, err := provider.UpTo(ctx, 33); err != nil {
		t.Fatalf("migrate to 33: %v", err)
	}

	if name := orgName(t, pool, "org-sys"); name != "System" {
		t.Fatalf("org-sys name = %q, want System", name)
	}
	assertNoOrg(t, pool, "default")

	tables := orgIDTables(t, pool)
	for _, name := range tables {
		var left int
		if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE org_id = 'default'", name)).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Fatalf("%s left %d rows in the old org", name, left)
		}
	}
	// The seeded rows are on the new org, so the move was not a delete.
	for _, name := range []string{
		"workspaces", "tasks", "events", "resources", "step_executions", "access_policies",
		"installed_packages", "installed_package_requirements", "installed_package_contributions",
	} {
		var moved int
		if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE org_id = 'org-sys'", name)).Scan(&moved); err != nil {
			t.Fatal(err)
		}
		if moved == 0 {
			t.Fatalf("%s moved no rows to org-sys", name)
		}
	}
	// The new default is the renamed org, on exactly the tables that had one.
	for _, name := range tables {
		var def sql.NullString
		if err := pool.QueryRow(ctx, `SELECT column_default FROM information_schema.columns WHERE table_name = $1 AND column_name = 'org_id'`, name).Scan(&def); err != nil {
			t.Fatal(err)
		}
		want := slices.Contains(orgSysTables, name)
		got := def.Valid && strings.Contains(def.String, "'org-sys'")
		if got != want {
			t.Fatalf("%s org_id default = %q, want org-sys = %v", name, def.String, want)
		}
	}
	assertForeignKey(t, pool, "installed_package_requirements", "installed_package_requirements_org_id_required_name_fkey")
	assertForeignKey(t, pool, "installed_package_contributions", "installed_package_contributions_org_id_package_name_fkey")

	// The down migration is the exact reverse.
	if _, err := provider.DownTo(ctx, 32); err != nil {
		t.Fatalf("migrate down to 32: %v", err)
	}
	if name := orgName(t, pool, "default"); name != "Default" {
		t.Fatalf("default org name = %q, want Default", name)
	}
	assertNoOrg(t, pool, "org-sys")
}

// TestOrgOperatorUpgrade pins the operator phase: a fresh install and an
// existing one each end with exactly one person owner of org-sys, and running
// the upgrade again adds no second.
func TestOrgOperatorUpgrade(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)

	if err := db.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatalf("fresh upgrade: %v", err)
	}
	assertOneOperator(t, db.Pool)
	if err := db.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatalf("second upgrade: %v", err)
	}
	assertOneOperator(t, db.Pool)

	// An existing install that ran the first two phases but never the operator
	// phase: drop the phase's ledger row and what it wrote, then upgrade again.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM org_upgrades WHERE phase = 'operator'`); err != nil {
		t.Fatalf("reset operator phase: %v", err)
	}
	for _, sql := range []string{
		`DELETE FROM memberships WHERE identity_id = $1`,
		`DELETE FROM identities WHERE id = $1`,
	} {
		if _, err := db.Pool.Exec(ctx, sql, string(identity.OperatorID())); err != nil {
			t.Fatalf("reset operator phase: %v", err)
		}
	}
	if err := db.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatalf("upgrade on an existing install: %v", err)
	}
	assertOneOperator(t, db.Pool)
}

func assertOneOperator(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var owners int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM memberships m
		JOIN identities i ON i.id = m.identity_id
		WHERE m.org_id = 'org-sys' AND m.workspace_id IS NULL AND m.role = 'owner' AND i.kind = 'user'
	`).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("person owners of org-sys = %d, want 1", owners)
	}
	var kind string
	if err := pool.QueryRow(t.Context(), `SELECT kind FROM identities WHERE id = $1`, string(identity.OperatorID())).Scan(&kind); err != nil {
		t.Fatalf("operator identity: %v", err)
	}
	if kind != string(identity.KindUser) {
		t.Fatalf("operator kind = %q, want %q", kind, identity.KindUser)
	}
}

func seedDefaultOrg(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	statements := []string{
		`INSERT INTO orgs (id, name) VALUES ('default', 'Default')`,
		`INSERT INTO workspaces (id, org_id, name) VALUES ('default', 'default', 'Default')`,
		`INSERT INTO tasks (owner, repo, issue_number, identity, org_id, workspace_id) VALUES ('o', 'r', 1, 'sam', 'default', 'default')`,
		`INSERT INTO events (at, kind, org_id, workspace_id) VALUES (now(), 'task_started', 'default', 'default')`,
		`INSERT INTO resources (org_id, kind, value, version, updated_at) VALUES ('default', 'workflows', '\x00', 1, now())`,
		`INSERT INTO step_executions (org_id, execution_id, attempt, kind, name) VALUES ('default', 1, 1, 'stage', 'build')`,
		`INSERT INTO access_policies (level, org_id, policy_id, text) VALUES ('org', 'default', 'p', 'allow')`,
		`INSERT INTO installed_packages (org_id, name, reference, digest, descriptor, layer) VALUES ('default', 'pkg', 'ref', 'sha256:' || repeat('a', 64), '{}', '\x00')`,
		`INSERT INTO installed_packages (org_id, name, reference, digest, descriptor, layer) VALUES ('default', 'dep', 'ref', 'sha256:' || repeat('b', 64), '{}', '\x00')`,
		`INSERT INTO installed_package_requirements (org_id, package_name, required_name, required_digest) VALUES ('default', 'pkg', 'dep', 'sha256:' || repeat('b', 64))`,
		`INSERT INTO installed_package_contributions (org_id, package_name, family, entry_id) VALUES ('default', 'pkg', 'fam', 'e1')`,
	}
	for _, sql := range statements {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("seed %q: %v", sql, err)
		}
	}
}

// orgIDTables lists every table that carries an org_id column.
func orgIDTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT table_name FROM information_schema.columns WHERE column_name = 'org_id' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	return tables
}

func orgName(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(t.Context(), `SELECT name FROM orgs WHERE id = $1`, id).Scan(&name); err != nil {
		t.Fatalf("org %s: %v", id, err)
	}
	return name
}

func assertNoOrg(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM orgs WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("org %s still exists", id)
	}
}

func assertForeignKey(t *testing.T, pool *pgxpool.Pool, table, name string) {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND conrelid = $1::regclass AND conname = $2`, table, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("foreign key %s on %s = %d, want 1", name, table, n)
	}
}
