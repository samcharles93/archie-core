// Package main tests the offline recovery subcommands of archie-state-store:
// backup, restore, validate and rollback against the task database file
// directly. They are the operator's only path back when the control plane's
// stored settings will not validate, because the daemon fails closed: the
// in-band remedy (replay an earlier revision while the State Store is up) is
// unavailable in exactly the case that needs it.
//
// The tests drive the subcommands the way an operator does -- through
// runRecovery, against real files on disk -- rather than through the internal
// helpers, so the flag surface and the exit codes are part of what is verified.

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/samcharles93/archie-core/internal/app/archied"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
	"github.com/samcharles93/archie-core/internal/store"
)

// runRecoveryCmd runs one subcommand the way main does and returns its exit
// code with both streams captured.
func runRecoveryCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runRecovery(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// openTaskStore opens the task database the subcommands operate on. It does not
// take the ownership lock: only the serving process does that, and a test that
// wants a "live" store holds it explicitly with holdStoreOwnership.
func openTaskStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open task store %s: %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// holdStoreOwnership takes the ownership lock the running State Store holds for
// its whole life, so a test can prove a command refuses to touch a live store.
func holdStoreOwnership(t *testing.T, path string) {
	t.Helper()
	ownership, err := store.AcquireOwnership(path)
	if err != nil {
		t.Fatalf("acquire store ownership: %v", err)
	}
	t.Cleanup(func() { _ = ownership.Release() })
}

// seedTask writes one task so a snapshot can be distinguished from the live
// database by what it contains, not merely by its existence.
func seedTask(t *testing.T, st *store.Store, title string) {
	t.Helper()
	if _, err := st.EnqueueChatTask(t.Context(), "acme", "widget", title, "body", "implement", ""); err != nil {
		t.Fatalf("seed task %q: %v", title, err)
	}
}

func taskTitles(t *testing.T, path string) []string {
	t.Helper()
	st := openTaskStore(t, path)
	tasks, err := st.Tasks(t.Context(), 100)
	if err != nil {
		t.Fatalf("list tasks in %s: %v", path, err)
	}
	titles := make([]string, 0, len(tasks))
	for _, task := range tasks {
		titles = append(titles, task.Title)
	}
	return titles
}

// backupPath is the snapshot every restore test restores from.
func backupPath(t *testing.T, dir string) string { return filepath.Join(dir, "snapshot.sqlite") }

// backup takes the snapshot the caller then restores, through the subcommand
// under test rather than through the store directly, so a broken backup fails
// the restore test too instead of silently passing an empty file.
func backup(t *testing.T, db, out string) {
	t.Helper()
	code, _, stderr := runRecoveryCmd(t, "backup", "-db", db, "-out", out)
	if code != 0 {
		t.Fatalf("backup exited %d: %s", code, stderr)
	}
}

// The update installer cannot stop the process that runs it, so it backs the
// task database up while the State Store is serving. VACUUM INTO is safe
// against a live writer; a backup that insisted on an exclusively-owned file
// would break the one caller it has, and the update would refuse to start.
func TestRecoveryBackupSnapshotsAStoreItsStateStoreIsServing(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	out := backupPath(t, dir)
	seedTask(t, openTaskStore(t, db), "before")
	holdStoreOwnership(t, db)
	if err := os.WriteFile(out, []byte("stale snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runRecoveryCmd(t, "backup", "-db", db, "-out", out)
	if code != 0 {
		t.Fatalf("backup exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, out) || !strings.Contains(stdout, db) {
		t.Errorf("backup must report the database and the snapshot it wrote; stdout = %q", stdout)
	}
	// The stale file was replaced by a real snapshot, and the snapshot is a
	// store a restore can put back: same rows, readable through the store.
	if got := taskTitles(t, out); len(got) != 1 || got[0] != "before" {
		t.Fatalf("snapshot tasks = %v, want the one seeded task", got)
	}
	if entries, err := filepath.Glob(out + ".tmp*"); err != nil || len(entries) != 0 {
		t.Errorf("backup left scratch files beside the snapshot: %v (%v)", entries, err)
	}
}

// The snapshot is the rollback state, so an interrupted backup must never
// leave a truncated file where a good snapshot was.
func TestRecoveryBackupRefusesAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runRecoveryCmd(t, "backup", "-db", filepath.Join(dir, "absent.sqlite"), "-out", backupPath(t, dir))
	if code == 0 {
		t.Fatal("backup of a missing database succeeded")
	}
	if !strings.Contains(stderr, "absent.sqlite") {
		t.Errorf("refusal must name the database it could not read; stderr = %q", stderr)
	}
}

// backup is the one recovery command allowed to run against a serving store,
// and that exemption is only sound while it never replaces the database. With
// -out naming the database it renames the snapshot over the live file and
// leaves the WAL of the database it just unlinked -- the state the restore path
// refuses up front and the code elsewhere calls unrecoverable.
func TestRecoveryBackupRefusesTheDatabaseAsItsOwnSnapshot(t *testing.T) {
	db := filepath.Join(t.TempDir(), "archie.db-tasks.sqlite")
	st := openTaskStore(t, db)
	seedTask(t, st, "live")

	code, _, stderr := runRecoveryCmd(t, "backup", "-db", db, "-out", db)
	if code == 0 {
		t.Fatal("backup over the database itself succeeded")
	}
	if !strings.Contains(stderr, "is the database itself") {
		t.Errorf("refusal must say the snapshot is the database; stderr = %q", stderr)
	}
	if got := taskTitles(t, db); len(got) != 1 || got[0] != "live" {
		t.Fatalf("refused backup changed the database: tasks = %v", got)
	}
}

// Restoring is the forward-migration escape hatch: the previous release
// reads this file, so it must be exactly the snapshot, with the WAL that
// belongs to the replaced database gone rather than replayed over it.
func TestRecoveryRestorePutsTheSnapshotBackAndDropsItsWAL(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	out := backupPath(t, dir)
	st := openTaskStore(t, db)
	seedTask(t, st, "before")
	backup(t, db, out)
	seedTask(t, st, "after")

	code, stdout, stderr := runRecoveryCmd(t, "restore", "-db", db, "-from", out)
	if code != 0 {
		t.Fatalf("restore exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, out) {
		t.Errorf("restore must report the snapshot it restored from; stdout = %q", stdout)
	}
	// Checked before anything reopens the database: opening a store sets WAL
	// mode, which creates these files, so the point is that the restore did not
	// leave the replaced database's log behind -- not that a later reader
	// cannot recreate its own.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(db + suffix); err == nil {
			t.Errorf("%s survived the restore: the replaced database's write-ahead log must not be replayed over the snapshot", db+suffix)
		}
	}
	if got := taskTitles(t, db); len(got) != 1 || got[0] != "before" {
		t.Fatalf("restored tasks = %v, want only the snapshot's task", got)
	}
}

// Overwriting a database a running State Store still owns leaves that process
// writing to an unlinked inode while the store serves a file nobody owns --
// the one way an offline command can destroy a task store.
func TestRecoveryRestoreRefusesAStoreTheStateStoreIsRunning(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	out := backupPath(t, dir)
	st := openTaskStore(t, db)
	seedTask(t, st, "before")
	backup(t, db, out)
	seedTask(t, st, "after")
	holdStoreOwnership(t, db)

	code, _, stderr := runRecoveryCmd(t, "restore", "-db", db, "-from", out)
	if code == 0 {
		t.Fatal("restore against a live State Store succeeded")
	}
	if !strings.Contains(stderr, "owned by another process") {
		t.Errorf("refusal must say the database is owned by another process; stderr = %q", stderr)
	}
	if got := taskTitles(t, db); len(got) != 2 {
		t.Fatalf("refused restore changed the database: tasks = %v", got)
	}
}

func TestRecoveryRestoreRefusesAFileThatIsNotAStore(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	seedTask(t, openTaskStore(t, db), "live")
	notAStore := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notAStore, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runRecoveryCmd(t, "restore", "-db", db, "-from", notAStore)
	if code == 0 {
		t.Fatal("restore from a file that is not a store succeeded")
	}
	if !strings.Contains(stderr, notAStore) {
		t.Errorf("refusal must name the snapshot it could not read; stderr = %q", stderr)
	}
	if got := taskTitles(t, db); len(got) != 1 {
		t.Fatalf("refused restore changed the database: tasks = %v", got)
	}
}

func TestRecoveryRestoreRefusesTheDatabaseAsItsOwnSnapshot(t *testing.T) {
	// Restoring a database from itself would delete the write-ahead log of the
	// file it is about to copy, so the one case that must never reach the swap
	// is refused up front.
	db := filepath.Join(t.TempDir(), "archie.db-tasks.sqlite")
	seedTask(t, openTaskStore(t, db), "live")

	code, _, stderr := runRecoveryCmd(t, "restore", "-db", db, "-from", db)
	if code == 0 {
		t.Fatal("restore from the database itself succeeded")
	}
	if !strings.Contains(stderr, "is the database itself") {
		t.Errorf("refusal must say the snapshot is the database; stderr = %q", stderr)
	}
	if got := taskTitles(t, db); len(got) != 1 {
		t.Fatalf("refused restore changed the database: tasks = %v", got)
	}
}

func TestRecoveryValidateAcceptsAHealthyStore(t *testing.T) {
	dir := t.TempDir()
	configPath := writeMinimalConfig(t, dir)
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	st := openTaskStore(t, db)
	seedTask(t, st, "healthy")
	seedStoreResources(t, st, configPath)

	code, stdout, stderr := runRecoveryCmd(t, "validate", "-db", db, "-config", configPath)
	if code != 0 {
		t.Fatalf("validate exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "schema version") {
		t.Errorf("validate must report the schema version it verified; stdout = %q", stdout)
	}
	// The count is part of what the operator reads, and a check that reports
	// "0 stored resources" for a store full of them is worse than no check.
	if count := storedResourceCount(t, stdout); count == 0 {
		t.Errorf("validate reported no stored resources for a seeded store; stdout = %q", stdout)
	}
}

// validate's verdict is only useful if it is the daemon's verdict. Boot refuses
// on configuration.Validate over the file config with every stored resource
// layered onto it (internal/app/archied/control_plane.go), not on the write
// path's own per-resource decode, and the two disagree: the write path accepts
// a scheduling policy boot rejects.
func TestRecoveryValidateRunsTheGateBootRuns(t *testing.T) {
	dir := t.TempDir()
	configPath := writeMinimalConfig(t, dir)
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	seedStoreResources(t, openTaskStore(t, db), configPath)

	// The control: a store seeded from this config is one the daemon boots on.
	code, _, stderr := runRecoveryCmd(t, "validate", "-db", db, "-config", configPath)
	if code != 0 {
		t.Fatalf("validate exited %d on a store the daemon starts on: %s", code, stderr)
	}

	// A hand-edited value, or an older revision of one: the write path's own
	// validator never inspects dispatch.trigger, so only the config gate
	// catches it, and the daemon exits 1 on it (validateDispatch).
	if err := execRaw(t, db, `UPDATE resources SET value='{"poll_interval":"1m","max_retries":3,"dispatch":{"trigger":"bogus"}}' WHERE kind='scheduling-policy'`); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runRecoveryCmd(t, "validate", "-db", db, "-config", configPath)
	if code == 0 {
		t.Fatalf("validate accepted a stored policy the daemon refuses to boot with; stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "dispatch.trigger") {
		t.Errorf("refusal must name what boot rejects; stderr = %q", stderr)
	}

	// A store the State Store never seeded holds no kinds at all. Its next start
	// creates the resources table and seeds every kind from this same config
	// (internal/app/archied/state_store.go), so archied layers those seeds and
	// starts: refusing the file here would send an operator to restore a
	// snapshot for a store the daemon boots on perfectly well.
	unseeded := filepath.Join(dir, "unseeded.db-tasks.sqlite")
	openTaskStore(t, unseeded)
	code, stdout, stderr = runRecoveryCmd(t, "validate", "-db", unseeded, "-config", configPath)
	if code != 0 {
		t.Fatalf("validate refused a store the State Store would seed on its next start; stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "0 stored resources validate") {
		t.Errorf("validate must report that there was nothing stored to check; stdout = %q", stdout)
	}

	// The upgrade case: a release defines a kind the store does not hold yet,
	// and validate runs before the State Store has started again. The State
	// Store seeds the kind it is missing, so this is the same stance as the
	// store with nothing stored at all -- the file is not the reason the daemon
	// would refuse to start, and saying otherwise costs the operator a snapshot.
	partial := filepath.Join(dir, "partial.db-tasks.sqlite")
	seedStoreResources(t, openTaskStore(t, partial), configPath)
	if err := execRaw(t, partial, `DELETE FROM resources WHERE kind='provider-settings'`); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runRecoveryCmd(t, "validate", "-db", partial, "-config", configPath)
	if code != 0 {
		t.Fatalf("validate refused a store missing a kind the State Store seeds; stderr = %q", stderr)
	}
	if count := storedResourceCount(t, stdout); count == 0 {
		t.Errorf("validate must still check the kinds the store does hold; stdout = %q", stdout)
	}
}

// The subcommands advertise -h as the way to read their flags, and the serve
// path in the same binary exits 0 for the same request, so a script probing the
// recovery surface must not read a requested help as a failure.
func TestRecoveryHelpExitsZero(t *testing.T) {
	commands := []string{archied.RecoveryBackup, archied.RecoveryRestore, archied.RecoveryValidate, archied.RecoveryRollback}
	for _, command := range commands {
		code, _, stderr := runRecoveryCmd(t, command, "-h")
		if code != 0 {
			t.Errorf("%s -h exited %d, want 0; stderr = %q", command, code, stderr)
		}
	}
}

// validate diagnoses a deployment; it is not the deployment. Resolving the boot
// config must not create, append to, or rotate the daemon's log file or the
// directory it lives in: a line stamped component="daemon" in archied's log is
// indistinguishable from the daemon having written it.
func TestRecoveryValidateLeavesTheDaemonLogAlone(t *testing.T) {
	dir := t.TempDir()
	configPath, logFile := writeConfigWithLogFile(t, dir)
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	seedStoreResources(t, openTaskStore(t, db), configPath)

	code, _, stderr := runRecoveryCmd(t, "validate", "-db", db, "-config", configPath)
	if code != 0 {
		t.Fatalf("validate exited %d: %s", code, stderr)
	}
	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Fatalf("validate touched the daemon's log file %s (stat err = %v)", logFile, err)
	}
	if _, err := os.Stat(filepath.Dir(logFile)); !os.IsNotExist(err) {
		t.Errorf("validate created the log directory %s (stat err = %v)", filepath.Dir(logFile), err)
	}
}

// validate's verdict is the daemon's verdict only if it reads the configuration
// the daemon reads. archied resolves that default from its own configHome, which
// honours a relative XDG_CONFIG_HOME; os.UserConfigDir rejects a relative one
// outright, which left the command with no config and no verdict at all.
func TestRecoveryDefaultConfigFollowsTheDaemonsConfigHome(t *testing.T) {
	relative := filepath.Join("relative", "config")
	t.Setenv("XDG_CONFIG_HOME", relative)
	if got, want := defaultConfigPath(), filepath.Join(relative, "archie", "config.toml"); got != want {
		t.Errorf("defaultConfigPath() with a relative XDG_CONFIG_HOME = %q, want %q", got, want)
	}

	absolute := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", absolute)
	if got, want := defaultConfigPath(), filepath.Join(absolute, "archie", "config.toml"); got != want {
		t.Errorf("defaultConfigPath() = %q, want %q", got, want)
	}
	// The equality itself: one helper, so the two cannot drift apart again.
	if got, want := defaultConfigPath(), archied.DefaultConfigPath(); got != want {
		t.Errorf("defaultConfigPath() = %q, but the daemon resolves %q", got, want)
	}
}

// validate exists to answer "would archied start against this file". Each case
// below is a state the daemon fails closed on, or a file it cannot read at all.
func TestRecoveryValidateRefusesWhatTheDaemonRefuses(t *testing.T) {
	newStore := func(t *testing.T) string {
		t.Helper()
		db := filepath.Join(t.TempDir(), "archie.db-tasks.sqlite")
		openTaskStore(t, db)
		return db
	}

	t.Run("forward_migrated_schema", func(t *testing.T) {
		// A database written by a newer release is the state a rollback has to
		// recognise before it hands the file to an older binary.
		db := newStore(t)
		if err := execRaw(t, db, `PRAGMA user_version = 99`); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runRecoveryCmd(t, "validate", "-db", db)
		if code == 0 {
			t.Fatal("validate accepted a forward-migrated database")
		}
		if !strings.Contains(stderr, "newer than supported version") {
			t.Errorf("refusal must name the schema version; stderr = %q", stderr)
		}
	})

	t.Run("corrupt_file", func(t *testing.T) {
		// A real database header over pages that are not a database. The magic
		// is right, so this fails on the content rather than on the format, which
		// is the shape a half-written or truncated store has.
		db := filepath.Join(t.TempDir(), "archie.db-tasks.sqlite")
		corrupt := append([]byte("SQLite format 3\x00"), bytes.Repeat([]byte{0xff}, 4096)...)
		if err := os.WriteFile(db, corrupt, 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runRecoveryCmd(t, "validate", "-db", db)
		if code == 0 {
			t.Fatal("validate accepted a corrupt database")
		}
		if !strings.Contains(stderr, db) {
			t.Errorf("refusal must name the file; stderr = %q", stderr)
		}
	})

	t.Run("not_a_database", func(t *testing.T) {
		// Pointing -db at the wrong file is the common operator mistake, and the
		// configured db_path is a different file from the task database the
		// State Store owns.
		db := filepath.Join(t.TempDir(), "archie.db")
		if err := os.WriteFile(db, []byte("db_path, not the task database"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runRecoveryCmd(t, "validate", "-db", db)
		if code == 0 {
			t.Fatal("validate accepted a file that is not a database")
		}
		if !strings.Contains(stderr, db) {
			t.Errorf("refusal must name the file; stderr = %q", stderr)
		}
	})

	t.Run("stored_resource_that_will_not_validate", func(t *testing.T) {
		// A stored value that both gates refuse: the write path stops it at
		// Decode (workflow.ExecutionSettings.Validate rejects a negative limit),
		// and boot stops on it too -- startWorkflowExecutionSettings decodes the
		// same document and exits 1. It can only get here by bypassing the write
		// path, which is exactly the state the operator has no path back from.
		db := newStore(t)
		seedResource(t, openTaskStore(t, db), controlplane.WorkflowExecutionSettingsKind,
			`{"max_model_tool_steps":-1,"max_runtime_seconds":60,"max_consecutive_gate_failures":3}`, "hand-edited")
		code, _, stderr := runRecoveryCmd(t, "validate", "-db", db)
		if code == 0 {
			t.Fatal("validate accepted a stored resource the write path would refuse")
		}
		if !strings.Contains(stderr, controlplane.WorkflowExecutionSettingsKind) {
			t.Errorf("refusal must name the resource kind; stderr = %q", stderr)
		}
	})

	t.Run("missing_database", func(t *testing.T) {
		db := newStore(t)
		if err := os.Remove(db); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runRecoveryCmd(t, "validate", "-db", db)
		if code == 0 {
			t.Fatal("validate accepted a database that does not exist")
		}
		if !strings.Contains(stderr, db) {
			t.Errorf("refusal must name the file; stderr = %q", stderr)
		}
	})

	t.Run("store_without_the_control_plane_table", func(t *testing.T) {
		// A store written before the control plane existed holds no stored
		// settings. The serving process creates the table on its next start, so
		// refusing this file would send an operator to restore a snapshot for a
		// database that would have started perfectly well.
		dir := t.TempDir()
		configPath := writeMinimalConfig(t, dir)
		db := filepath.Join(dir, "archie.db-tasks.sqlite")
		openTaskStore(t, db)
		if err := execRaw(t, db, `DROP TABLE resources`); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runRecoveryCmd(t, "validate", "-db", db, "-config", configPath)
		if code != 0 {
			t.Fatalf("validate exited %d: %s", code, stderr)
		}
		if !strings.Contains(stdout, "0 stored resources validate") {
			t.Errorf("validate must report that there was nothing stored to check; stdout = %q", stdout)
		}
	})
}

// rollback is the one operation that closes the documented gap: with the State
// Store stopped, it replays the value an earlier revision recorded through the
// ordinary replace, so no rollback RPC is needed and the rollback itself is
// audited as one more revision.
func TestRecoveryRollbackReplaysTheRevisionThroughReplace(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	st := openTaskStore(t, db)
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"anthropic/claude"}`, "first")
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"openai/gpt"}`, "second")

	code, stdout, stderr := runRecoveryCmd(t, "rollback", "-db", db, "-kind", controlplane.ModelRoleAssignmentsKind)
	if code != 0 {
		t.Fatalf("rollback exited %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, controlplane.ModelRoleAssignmentsKind) {
		t.Errorf("rollback must name the resource it replayed; stdout = %q", stdout)
	}
	resource, err := st.Resource(t.Context(), controlplane.ModelRoleAssignmentsKind)
	if err != nil {
		t.Fatal(err)
	}
	if string(resource.Value) != `{"implement":"anthropic/claude"}` {
		t.Errorf("resource value = %s, want the first revision's value", resource.Value)
	}
	if resource.Version != 3 {
		t.Errorf("resource version = %d, want a new revision (3) rather than a rewrite of the old one", resource.Version)
	}
	history, err := st.ResourceHistory(t.Context(), controlplane.ModelRoleAssignmentsKind, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history has %d revisions, want the rollback recorded as the third", len(history))
	}
	if history[0].Source != archied.OfflineRollbackSource {
		t.Errorf("newest revision source = %q, want %q so the rollback is auditable", history[0].Source, archied.OfflineRollbackSource)
	}
}

// The revision to restore may be named, which is how an operator reaches past
// the most recent change without replaying it first.
func TestRecoveryRollbackRestoresANamedRevision(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "archie.db-tasks.sqlite")
	st := openTaskStore(t, db)
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"anthropic/claude"}`, "first")
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"openai/gpt"}`, "second")
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"google/gemini"}`, "third")

	code, _, stderr := runRecoveryCmd(t, "rollback", "-db", db, "-kind", controlplane.ModelRoleAssignmentsKind, "-revision", "1")
	if code != 0 {
		t.Fatalf("rollback exited %d: %s", code, stderr)
	}
	resource, err := st.Resource(t.Context(), controlplane.ModelRoleAssignmentsKind)
	if err != nil {
		t.Fatal(err)
	}
	if string(resource.Value) != `{"implement":"anthropic/claude"}` {
		t.Errorf("resource value = %s, want the value recorded at revision 1", resource.Value)
	}
}

func TestRecoveryRollbackRefusesWhatItCannotReplay(t *testing.T) {
	newStore := func(t *testing.T, revisions int) string {
		t.Helper()
		db := filepath.Join(t.TempDir(), "archie.db-tasks.sqlite")
		st := openTaskStore(t, db)
		for i := range revisions {
			value := `{"implement":"anthropic/claude"}`
			if i > 0 {
				value = `{"implement":"openai/gpt"}`
			}
			seedResource(t, st, controlplane.ModelRoleAssignmentsKind, value, "seed")
		}
		return db
	}

	t.Run("unknown_kind", func(t *testing.T) {
		db := newStore(t, 2)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-db", db, "-kind", "not-a-resource")
		if code == 0 {
			t.Fatal("rollback of an unknown resource kind succeeded")
		}
		if !strings.Contains(stderr, "not-a-resource") {
			t.Errorf("refusal must name the kind; stderr = %q", stderr)
		}
	})

	t.Run("no_earlier_revision", func(t *testing.T) {
		db := newStore(t, 1)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-db", db, "-kind", controlplane.ModelRoleAssignmentsKind)
		if code == 0 {
			t.Fatal("rollback with no earlier revision succeeded")
		}
		if !strings.Contains(stderr, "no earlier revision") {
			t.Errorf("refusal must say there is nothing to roll back to; stderr = %q", stderr)
		}
	})

	t.Run("current_revision", func(t *testing.T) {
		db := newStore(t, 2)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-db", db, "-kind", controlplane.ModelRoleAssignmentsKind, "-revision", "2")
		if code == 0 {
			t.Fatal("rollback of the current revision succeeded")
		}
		if !strings.Contains(stderr, "is the current version") {
			t.Errorf("refusal must say the revision is already the current value; stderr = %q", stderr)
		}
	})

	t.Run("live_state_store", func(t *testing.T) {
		db := newStore(t, 2)
		holdStoreOwnership(t, db)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-db", db, "-kind", controlplane.ModelRoleAssignmentsKind)
		if code == 0 {
			t.Fatal("rollback against a live State Store succeeded")
		}
		if !strings.Contains(stderr, "owned by another process") {
			t.Errorf("refusal must say the database is owned by another process; stderr = %q", stderr)
		}
	})
}

// A positional argument that is not a subcommand used to be ignored, leaving
// an operator who mistyped a command watching a server start instead.
func TestRecoveryRejectsAnUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRecovery([]string{"backp", "-db", "x"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("an unknown subcommand exited 0")
	}
	if !strings.Contains(stderr.String(), "backp") {
		t.Errorf("refusal must name the unknown subcommand; stderr = %q", stderr.String())
	}
}

// writeConfigWithLogFile is writeMinimalConfig plus the daemon's durable log
// destination, which only the daemon may bring into existence.
func writeConfigWithLogFile(t *testing.T, dir string) (string, string) {
	t.Helper()
	configPath := writeMinimalConfig(t, dir)
	logFile := filepath.Join(dir, "logs", "archied.log")
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open config to append: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("close config: %v", err)
		}
	}()
	if _, err := fmt.Fprintf(f, "\n[log]\nfile = %q\n", logFile); err != nil {
		t.Fatalf("append [log]: %v", err)
	}
	return configPath, logFile
}

// seedStoreResources seeds the store the way the State Store seeds it at boot,
// from the same config file the operator hands validate: the command's verdict
// is about the pair, and a store whose settings never matched the config is a
// state boot does not recognise either.
//
// The seeding builds its control-plane server from the shared provider set
// (internal/infrastructure/workflowsteps), which is what every composition root
// registers -- including openStateStoreControlPlane, the root helper the
// recovery path uses. This test cannot call that unexported helper, so it
// registers the same set itself rather than an empty manager: the definitions
// are decoded with the vocabulary the served path validates against, which is
// what makes the seeded values the ones validate is asked about.
func seedStoreResources(t *testing.T, st *store.Store, configPath string) {
	t.Helper()
	doc, err := configuration.New(nil).Resolve(configPath, "")
	if err != nil {
		t.Fatalf("resolve %s: %v", configPath, err)
	}
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatalf("register workflow step vocabulary: %v", err)
	}
	server, err := controlplane.NewServer(st, steps)
	if err != nil {
		t.Fatalf("build control plane server: %v", err)
	}
	if _, err := server.ImportConfig(t.Context(), doc.Config); err != nil {
		t.Fatalf("seed store resources: %v", err)
	}
}

// storedResourceCount reads the count validate reports out of its summary line.
func storedResourceCount(t *testing.T, stdout string) int {
	t.Helper()
	before, found := strings.CutSuffix(stdout, " stored resources validate\n")
	if !found {
		t.Fatalf("no resource count in %q", stdout)
	}
	fields := strings.Fields(before)
	count, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		t.Fatalf("resource count in %q: %v", stdout, err)
	}
	return count
}

// seedResource writes one revision of a control-plane resource through the
// store, the way the control plane's replace does. The request ID is derived
// from the label and the revision it produces: the store deduplicates a
// repeated request ID, so a fixed one would make the second write of a series
// a silent no-op.
func seedResource(t *testing.T, st *store.Store, kind, value, label string) {
	t.Helper()
	current, err := st.Resource(t.Context(), kind)
	expected := int64(0)
	if err == nil {
		expected = current.Version
	}
	var document any
	if err := json.Unmarshal([]byte(value), &document); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutResource(t.Context(), store.ResourceWrite{
		Kind: kind, Value: encoded, Actor: label, Source: "test",
		RequestID: fmt.Sprintf("%s-%d", label, expected+1), ExpectedVersion: expected,
	}); err != nil {
		t.Fatalf("seed resource %s: %v", kind, err)
	}
}

// execRaw runs one statement against the database file the way an operator
// editing a broken store would, bypassing every check the store applies.
func execRaw(t *testing.T, path, statement string) error {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(t.Context(), statement)
	return err
}
