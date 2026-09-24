// Package legacyimport moves an existing install's SQLite stores (the State
// Store's task database, the PocketBase event store and the Gateway
// conversation store) into Postgres, once.
//
// The import is one transaction: it refuses a target that already holds data
// in a domain it imports, refuses rather than skips a row the target cannot
// hold, verifies what it wrote against what it read, and only then records a
// completion row and commits. A refusal at any point leaves the target as it
// was, migrated and empty, so a failed import is never serveable.
//
// It reads through legacyread, which never writes to the source files, and is
// deleted together with it.
package legacyimport

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyread"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// ErrRefused wraps every refusal. The message names each offending table,
// row, value and rule.
var ErrRefused = errors.New("legacy import refused")

// Sources are the legacy files. A path whose file does not exist means that
// domain has nothing to import.
type Sources struct {
	StateStore string
	EDA        string
	Gateway    string
}

type domain struct {
	name   string
	path   string
	tables []legacyread.Table
}

func (s Sources) domains() []domain {
	return []domain{
		{"state-store", s.StateStore, legacyread.StateStoreTables},
		{"eda", s.EDA, legacyread.EDATables},
		{"gateway", s.Gateway, legacyread.GatewayTables},
	}
}

// present returns the domains whose source file exists.
func (s Sources) present() ([]domain, error) {
	var out []domain
	for _, d := range s.domains() {
		if d.path == "" {
			continue
		}
		_, err := os.Stat(d.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("legacy import: %w", err)
		}
		out = append(out, d)
	}
	return out, nil
}

// HasLegacyData reports whether any legacy source holds a row. With
// postgres.ImportComplete it is what decides whether a Postgres database may
// serve: legacy data without a completed import must be refused.
func HasLegacyData(ctx context.Context, src Sources) (bool, error) {
	for _, d := range src.domains() {
		if d.path == "" {
			continue
		}
		has, err := legacyread.HasData(ctx, d.path, d.tables)
		if err != nil || has {
			return has, err
		}
	}
	return false, nil
}

// Report is what an import wrote.
type Report struct {
	// Rows is each imported table's row count.
	Rows map[string]int
	// UnindexedMessages are Gateway message ids the source's FTS5 index did
	// not cover. The target derives its search column from every row, so
	// these become searchable; the count says the source index was stale.
	UnindexedMessages []string
}

func (r Report) String() string {
	var b strings.Builder
	for _, table := range slices.Sorted(maps.Keys(r.Rows)) {
		fmt.Fprintf(&b, "%s=%d ", table, r.Rows[table])
	}
	fmt.Fprintf(&b, "unindexed_messages=%d", len(r.UnindexedMessages))
	return b.String()
}

// refusal collects every problem found before giving up, so an operator fixes
// a source in one pass rather than one row per attempt.
type refusal []string

func (r *refusal) add(format string, args ...any) { *r = append(*r, fmt.Sprintf(format, args...)) }

func (r *refusal) err() error {
	if len(*r) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  %s", ErrRefused, strings.Join(*r, "\n  "))
}

// Import runs the one-time import from src into the database behind pool.
// Every service that serves the database must be stopped: the import holds
// each service's ownership claim for its whole run and refuses if any is
// taken.
func Import(ctx context.Context, pool *pgxpool.Pool, src Sources) (Report, error) {
	present, err := src.present()
	if err != nil {
		return Report{}, err
	}
	if len(present) == 0 {
		return Report{}, fmt.Errorf("%w: no legacy source files found (%s, %s, %s)", ErrRefused, src.StateStore, src.EDA, src.Gateway)
	}
	before, err := hashSources(present)
	if err != nil {
		return Report{}, err
	}
	if err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		return Report{}, err
	}
	release, err := claimServices(ctx, pool)
	defer release()
	if err != nil {
		return Report{}, err
	}
	sources, closeSources, err := openSources(ctx, present)
	defer closeSources()
	if err != nil {
		return Report{}, err
	}

	var problems refusal
	if err := checkSources(ctx, sources, &problems); err != nil {
		return Report{}, err
	}
	if err := problems.err(); err != nil {
		return Report{}, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("legacy import: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	report, err := importInto(ctx, tx, present, sources, &problems)
	if err != nil {
		return Report{}, err
	}
	if err := checkUnchanged(before, present, &problems); err != nil {
		return Report{}, err
	}
	if err := problems.err(); err != nil {
		return Report{}, err
	}
	if err := complete(ctx, tx, present, report); err != nil {
		return Report{}, err
	}
	return report, nil
}

// checkUnchanged refuses when a source's bytes moved during the import: a
// writer that was not stopped would leave the target short of its last writes.
func checkUnchanged(before map[string][32]byte, present []domain, problems *refusal) error {
	after, err := hashSources(present)
	if err != nil {
		return err
	}
	for path, sum := range before {
		if after[path] != sum {
			problems.add("%s changed while it was read: stop the process writing it and import again", path)
		}
	}
	return nil
}

// complete writes the completion record and commits, so the record exists
// exactly when the imported rows do.
func complete(ctx context.Context, tx pgx.Tx, present []domain, report Report) error {
	paths := make([]string, len(present))
	for i, d := range present {
		paths[i] = d.path
	}
	if err := postgresdb.New(tx).InsertImportCompletion(ctx, postgresdb.InsertImportCompletionParams{
		Sources: strings.Join(paths, "\n"),
		Report:  report.String(),
	}); err != nil {
		return fmt.Errorf("legacy import: completion record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("legacy import: commit: %w", err)
	}
	return nil
}

// claimServices takes every service's ownership claim without waiting. The
// returned release is safe to call whether or not the claims were taken.
func claimServices(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	var claims []*postgres.Ownership
	release := func() {
		for _, c := range claims {
			_ = c.Release(context.WithoutCancel(ctx))
		}
	}
	for _, name := range []string{postgres.OwnerStateStore, postgres.OwnerGateway} {
		claim, err := postgres.AcquireOwnership(ctx, pool, name)
		if err != nil {
			return release, fmt.Errorf("legacy import: stop every Archie service first: %w", err)
		}
		claims = append(claims, claim)
	}
	return release, nil
}

// openSources opens each present source read-only, keyed by domain name.
func openSources(ctx context.Context, present []domain) (map[string]*legacyread.Source, func(), error) {
	sources := map[string]*legacyread.Source{}
	closeAll := func() {
		for _, s := range sources {
			_ = s.Close()
		}
	}
	for _, d := range present {
		s, err := legacyread.Open(ctx, d.path)
		if err != nil {
			return nil, closeAll, err
		}
		sources[d.name] = s
	}
	return sources, closeAll, nil
}

// importInto loads and verifies every present domain inside tx, adding to
// problems anything that must stop the commit.
func importInto(ctx context.Context, tx pgx.Tx, present []domain, sources map[string]*legacyread.Source, problems *refusal) (Report, error) {
	if err := checkTarget(ctx, tx, present, problems); err != nil {
		return Report{}, err
	}
	if err := problems.err(); err != nil {
		return Report{}, err
	}
	report := Report{Rows: map[string]int{}}
	var loaded []loadedTable
	for _, d := range present {
		for _, t := range d.tables {
			lt, err := load(ctx, tx, sources[d.name], t, problems)
			if err != nil {
				return Report{}, err
			}
			loaded = append(loaded, lt)
			report.Rows[t.Name] = len(lt.keys)
		}
	}
	if err := problems.err(); err != nil {
		return Report{}, err
	}
	for _, lt := range loaded {
		if err := verify(ctx, tx, lt, problems); err != nil {
			return Report{}, err
		}
	}
	// Sources are derived, not imported: the migration derived them from an
	// empty target, so derive again from what was just loaded.
	if sources["eda"] != nil {
		if err := postgresdb.New(tx).DeriveSources(ctx); err != nil {
			return Report{}, fmt.Errorf("legacy import: derive sources: %w", err)
		}
	}
	if gw := sources["gateway"]; gw != nil {
		var err error
		if report.UnindexedMessages, err = unindexedMessages(ctx, gw, loaded); err != nil {
			return Report{}, err
		}
	}
	return report, nil
}

// hashSources fingerprints each source and its WAL, so a writer that was not
// stopped is caught rather than assumed away.
func hashSources(present []domain) (map[string][32]byte, error) {
	out := map[string][32]byte{}
	for _, d := range present {
		for _, path := range []string{d.path, d.path + "-wal"} {
			f, err := os.Open(path)
			if errors.Is(err, os.ErrNotExist) && path != d.path {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("legacy import: %w", err)
			}
			h := sha256.New()
			_, err = io.Copy(h, f)
			_ = f.Close()
			if err != nil {
				return nil, fmt.Errorf("legacy import: hash %s: %w", path, err)
			}
			out[path] = [32]byte(h.Sum(nil))
		}
	}
	return out, nil
}

// checkSources finds the source rows Postgres would reject or reorder.
func checkSources(ctx context.Context, sources map[string]*legacyread.Source, problems *refusal) error {
	if s := sources["state-store"]; s != nil {
		for _, fk := range legacyread.IdentityForeignKeys {
			orphans, err := s.Orphans(ctx, fk)
			if err != nil {
				return err
			}
			for _, o := range orphans {
				problems.add("%s %s: %s %q has no row in %s (foreign key)", fk.Table, o.Key, fk.Column, o.Parent, fk.Parent)
			}
		}
		aliases, err := s.DuplicateAliases(ctx)
		if err != nil {
			return err
		}
		for _, d := range aliases {
			problems.add("identity_aliases %s: aliases collide on unique lower(alias) %q", strings.Join(d.Values, ", "), d.Key)
		}
		collisions, err := s.MicrosecondCollisions(ctx)
		if err != nil {
			return err
		}
		for _, c := range collisions {
			problems.add("events %d and %d: at differs only below a microsecond (%s) while ids run the other way, so (at, id) order would change",
				c.EarlierID, c.LaterID, c.At.Format("2006-01-02T15:04:05.000000Z"))
		}
	}
	if s := sources["eda"]; s != nil {
		dups, err := s.DuplicateBindings(ctx)
		if err != nil {
			return err
		}
		for _, d := range dups {
			problems.add("bindings %s: more than one binding for source %q (unique source); resolve them and import again",
				strings.Join(d.Values, ", "), d.Key)
		}
	}
	return nil
}

// checkTarget refuses a database that already completed an import, or that
// holds rows in a table owned by a domain being imported. The check is an
// allowlist of those tables, so goose's version table and other domains'
// tables never count.
func checkTarget(ctx context.Context, tx pgx.Tx, present []domain, problems *refusal) error {
	done, err := postgresdb.New(tx).ImportCompleted(ctx)
	if err != nil {
		return fmt.Errorf("legacy import: completion record: %w", err)
	}
	if done {
		problems.add("this database already holds a completed import")
		return nil
	}
	for _, d := range present {
		for _, t := range d.tables {
			var rows bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+pgx.Identifier{t.Name}.Sanitize()+")").Scan(&rows); err != nil {
				return fmt.Errorf("legacy import: %s: %w", t.Name, err)
			}
			if rows {
				problems.add("%s is not empty: the %s target already holds data", t.Name, d.name)
			}
		}
	}
	return nil
}
