package archied

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
	"github.com/samcharles93/archie-core/internal/store"
)

// migrationServer builds the validating side exactly as RunStateStore composes
// it: a store over a throwaway file and openStateStoreControlPlane, the root
// helper that registers the shared step vocabulary.
func migrationServer(t *testing.T) (*controlplane.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "archie.db-tasks.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	server, err := openStateStoreControlPlane(st)
	if err != nil {
		t.Fatalf("open control plane: %v", err)
	}
	return server, st
}

// seedSchedules mirrors RunStateStore's ImportConfig: the schedules resource
// starts as the empty seed at version 1, which is the state the legacy
// migration runs against.
func seedSchedules(t *testing.T, control *controlplane.Server) int64 {
	t.Helper()
	seed, err := json.Marshal([]cronstore.JobSpec{})
	if err != nil {
		t.Fatalf("encode empty schedules seed: %v", err)
	}
	response, err := control.Command(t.Context(), &controlpb.CommandRequest{
		Kind: controlplane.SchedulesKind, Command: "replace", ValueJson: seed,
		ExpectedVersion: 0, Actor: "system:migration", Source: "legacy-config", RequestId: "import:schedules",
	})
	if err != nil {
		t.Fatalf("seed schedules: %v", err)
	}
	return response.GetResource().GetVersion()
}

// writeLegacyJobs writes a legacy cronstore file at the layout
// migrateLegacySchedules reads: <dir of dbPath>/cron/jobs.json, in the
// store's own on-disk envelope shape (schema v2).
func writeLegacyJobs(t *testing.T, dir string, jobs []cronstore.JobSpec) string {
	t.Helper()
	data, err := json.Marshal(struct {
		SchemaVersion int                 `json:"schema_version"`
		Jobs          []cronstore.JobSpec `json:"jobs"`
	}{SchemaVersion: 2, Jobs: jobs})
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return writeLegacyFile(t, dir, string(data))
}

func writeLegacyFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "cron", "jobs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create legacy cron directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy jobs file: %v", err)
	}
	return path
}

// legacyJob is a schema-v1-shaped job the old daemon's cron engine wrote.
// An empty Kind means chat, the pre-field default (cronstore.KindChat).
func legacyJob(id, kind string, interval time.Duration) cronstore.JobSpec {
	stamped := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	return cronstore.JobSpec{
		ID: id, Kind: kind, Detail: "legacy " + id,
		Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: cronstore.Duration(interval)},
		NextRun:  stamped, Created: stamped, Updated: stamped,
	}
}

// storedScheduleIDs decodes the schedules resource into the job IDs it holds.
func storedScheduleIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	resource, err := st.Resource(t.Context(), controlplane.SchedulesKind)
	if err != nil {
		t.Fatalf("read schedules resource: %v", err)
	}
	var jobs []cronstore.JobSpec
	if err := json.Unmarshal(resource.Value, &jobs); err != nil {
		t.Fatalf("decode schedules resource: %v", err)
	}
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return ids
}

// TestMigrateLegacySchedulesMovesOnlyWorkflowJobs: a legacy file holding a
// chat job (the pre-field default, kind omitted) and a workflow job migrates
// the workflow job and skips -- never refuses -- the chat one. The current
// behaviour replaces the whole list and lets the schedules validator fail the
// State Store on the chat job, taking every process that dials it down with
// it (archie-core-q6vw).
func TestMigrateLegacySchedulesMovesOnlyWorkflowJobs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "archie.db-tasks.sqlite")
	writeLegacyJobs(t, dir, []cronstore.JobSpec{
		legacyJob("chat-daily", "", 24*time.Hour),
		legacyJob("tdd-hourly", cronstore.KindWorkflow, time.Hour),
	})
	server, st := migrationServer(t)
	version := seedSchedules(t, server)

	migrateLegacySchedules(t.Context(), server, dbPath, version, discardLog())
	got := storedScheduleIDs(t, st)
	if len(got) != 1 || got[0] != "tdd-hourly" {
		t.Fatalf("schedules resource holds %v, want only the migratable workflow job", got)
	}
	if _, err := os.Stat(path_migrated(dir)); err != nil {
		t.Fatalf("migrated legacy file not marked (jobs.json.migrated): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cron", "jobs.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy file still at its original path after a successful migration")
	}
}

// path_migrated is where a completed migration leaves the legacy file.
func path_migrated(dir string) string {
	return filepath.Join(dir, "cron", "jobs.json.migrated")
}

// TestMigrateLegacySchedulesKeepsStoreRunningWhenNothingIsMigratable: a
// legacy file holding only chat jobs leaves the schedules resource untouched
// and reports -- never fails -- the skip. The store's whole job is to come up.
func TestMigrateLegacySchedulesKeepsStoreRunningWhenNothingIsMigratable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "archie.db-tasks.sqlite")
	path := writeLegacyJobs(t, dir, []cronstore.JobSpec{
		legacyJob("chat-daily", "", 24*time.Hour),
		legacyJob("chat-weekly", "chat", 7*24*time.Hour),
	})
	server, st := migrationServer(t)
	version := seedSchedules(t, server)

	migrateLegacySchedules(t.Context(), server, dbPath, version, discardLog())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file removed though nothing was migrated: %v", err)
	}
	if got := storedScheduleIDs(t, st); len(got) != 0 {
		t.Fatalf("schedules resource holds %v, want the untouched empty seed", got)
	}
}

// TestMigrateLegacySchedulesKeepsStoreRunningOnUnreadableLegacyStore: a
// corrupt legacy file is a diagnosis, not a boot blocker. The store must come
// up on the empty seed and the file must stay in place for the operator.
func TestMigrateLegacySchedulesKeepsStoreRunningOnUnreadableLegacyStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "archie.db-tasks.sqlite")
	path := writeLegacyFile(t, dir, "{not json")
	server, _ := migrationServer(t)
	version := seedSchedules(t, server)

	migrateLegacySchedules(t.Context(), server, dbPath, version, discardLog())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file removed though it was never read: %v", err)
	}
}

// TestMigrateLegacySchedulesLeavesTheFileWhenTheReplaceIsRefused: even a
// workflow job the schedules validator refuses is skipped and retried on the
// next start, the way ImportConfig skips a refused seed -- never fatal.
func TestMigrateLegacySchedulesLeavesTheFileWhenTheReplaceIsRefused(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "archie.db-tasks.sqlite")
	path := writeLegacyJobs(t, dir, []cronstore.JobSpec{
		legacyJob("broken-interval", cronstore.KindWorkflow, 0), // interval schedule with no interval
	})
	server, st := migrationServer(t)
	version := seedSchedules(t, server)

	migrateLegacySchedules(t.Context(), server, dbPath, version, discardLog())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file removed though the replace never landed: %v", err)
	}
	if got := storedScheduleIDs(t, st); len(got) != 0 {
		t.Fatalf("schedules resource holds %v, want the untouched empty seed", got)
	}
}

// discardLog returns a logger that drops every record: these tests pin the
// migration's effects on the resource and the file, not its reporting.
func discardLog() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
