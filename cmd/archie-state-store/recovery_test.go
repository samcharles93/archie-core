// Package main tests the offline recovery subcommands of archie-state-store
// against the PostgreSQL database the configuration names. They are the
// operator's only path back when the control plane's stored settings will not
// validate, because the daemon fails closed: the in-band remedy (replay an
// earlier revision while the State Store is up) is unavailable in exactly the
// case that needs it.
//
// The tests drive the subcommands the way an operator does -- through
// runRecovery -- rather than through the internal helpers, so the flag surface
// and the exit codes are part of what is verified.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/app/archied"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// runRecoveryCmd runs one subcommand the way main does and returns its exit
// code with both streams captured.
func runRecoveryCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runRecovery(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// recoveryFixture writes a config naming a fresh migrated database and returns
// it with the task database on that database.
func recoveryFixture(t *testing.T) (string, *pgstore.TaskDB) {
	t.Helper()
	url := pgtest.URL(t)
	configPath := writeConfigFor(t, t.TempDir(), url)
	return configPath, pgstore.On(pgstore.PoolAt(t, url))
}

// seedTask writes one task so the database holds more than its settings.
func seedTask(t *testing.T, st *pgstore.TaskDB, title string) {
	t.Helper()
	if _, err := st.EnqueueChatTask(t.Context(), "acme", "widget", title, "body", "implement", ""); err != nil {
		t.Fatalf("seed task %q: %v", title, err)
	}
}

// execRaw runs one statement against the database the way an operator editing
// a broken store would, bypassing every check the store applies.
func execRaw(t *testing.T, st *pgstore.TaskDB, statement string) {
	t.Helper()
	if _, err := st.Pool.Exec(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryValidateAcceptsAHealthyStore(t *testing.T) {
	configPath, st := recoveryFixture(t)
	seedTask(t, st, "healthy")
	seedStoreResources(t, st, configPath)

	code, stdout, stderr := runRecoveryCmd(t, "validate", "-config", configPath)
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
	configPath, st := recoveryFixture(t)
	seedStoreResources(t, st, configPath)

	// The control: a store seeded from this config is one the daemon boots on.
	code, _, stderr := runRecoveryCmd(t, "validate", "-config", configPath)
	if code != 0 {
		t.Fatalf("validate exited %d on a store the daemon starts on: %s", code, stderr)
	}

	// The upgrade case: a release defines a kind the store does not hold yet,
	// and validate runs before the State Store has started again. The State
	// Store seeds the kind it is missing, so the store is not the reason the
	// daemon would refuse to start.
	execRaw(t, st, `DELETE FROM resources WHERE kind='provider-settings'`)
	code, stdout, stderr := runRecoveryCmd(t, "validate", "-config", configPath)
	if code != 0 {
		t.Fatalf("validate refused a store missing a kind the State Store seeds; stderr = %q", stderr)
	}
	if count := storedResourceCount(t, stdout); count == 0 {
		t.Errorf("validate must still check the kinds the store does hold; stdout = %q", stdout)
	}

	// A hand-edited value, or an older revision of one: the write path's own
	// validator never inspects dispatch.trigger, so only the config gate
	// catches it, and the daemon exits 1 on it (validateDispatch).
	execRaw(t, st, `UPDATE resources SET value='{"poll_interval":"1m","max_retries":3,"dispatch":{"trigger":"bogus"}}' WHERE kind='scheduling-policy'`)
	code, stdout, stderr = runRecoveryCmd(t, "validate", "-config", configPath)
	if code == 0 {
		t.Fatalf("validate accepted a stored policy the daemon refuses to boot with; stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "dispatch.trigger") {
		t.Errorf("refusal must name what boot rejects; stderr = %q", stderr)
	}
}

// A database the State Store never seeded holds no kinds at all. Its next
// start seeds every kind from this same config, so archied layers those seeds
// and starts: refusing it would send an operator to restore a snapshot for a
// database the daemon boots on perfectly well.
func TestRecoveryValidateAcceptsAnUnseededDatabase(t *testing.T) {
	configPath, _ := recoveryFixture(t)
	code, stdout, stderr := runRecoveryCmd(t, "validate", "-config", configPath)
	if code != 0 {
		t.Fatalf("validate refused a store the State Store would seed on its next start; stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "0 stored resources validate") {
		t.Errorf("validate must report that there was nothing stored to check; stdout = %q", stdout)
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
	code, _, stderr := runRecoveryCmd(t, "validate", "-config", configPath)
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
// the daemon reads, and with no -config it reads whatever the one shared
// helper resolves. The resolution rule itself is pinned where it lives
// (internal/infrastructure/configuration); what only this test can pin is that
// the command takes its default from that helper rather than deriving it.
//
// A relative XDG_CONFIG_HOME is the case that separates the two: the command
// has to resolve ${XDG_CONFIG_HOME}/archie/config.toml relative to its working
// directory, and a second derivation through os.UserConfigDir rejects a
// relative value outright, leaving the command with no config to answer about.
func TestRecoveryDefaultConfigResolvesWhereTheDaemonReads(t *testing.T) {
	// The second layout is the teeth: the command has to read the config at the
	// resolved path, not merely find some config somewhere under the config home.
	// A resolution that drifts finds nothing and validate has no verdict.
	tests := []struct {
		name      string
		configDir string // relative to $XDG_CONFIG_HOME
		wantOK    bool
	}{
		{name: "$XDG_CONFIG_HOME/archie/config.toml", configDir: "archie", wantOK: true},
		{name: "a config elsewhere in the config home is not the default", configDir: "wrong-place"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("XDG_CONFIG_HOME", "xdg")

			configDir := filepath.Join(root, "xdg", tc.configDir)
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatalf("create %s: %v", configDir, err)
			}
			url := pgtest.URL(t)
			pgstore.PoolAt(t, url)
			configPath := writeConfigFor(t, configDir, url)
			code, stdout, stderr := runRecoveryCmd(t, "validate")
			if !tc.wantOK {
				if code == 0 {
					t.Fatalf("validate exited 0 with no config at the resolved path %s: %s", configPath, stdout)
				}
				return
			}
			if code != 0 {
				t.Fatalf("validate with no -config exited %d, so it did not read %s: %s", code, configPath, stderr)
			}
			if !strings.Contains(stdout, "stored resources validate") {
				t.Errorf("validate reported %q, want the verdict on the database the resolved config names", stdout)
			}
		})
	}
}

// validate exists to answer "would archied start against this database". A
// stored value that both gates refuse -- the write path stops it at Decode
// (workflow.ExecutionSettings.Validate rejects a negative limit) and boot stops
// on it too -- can only get here by bypassing the write path, which is exactly
// the state the operator has no path back from.
func TestRecoveryValidateRefusesWhatTheDaemonRefuses(t *testing.T) {
	configPath, st := recoveryFixture(t)
	seedResource(t, st, controlplane.WorkflowExecutionSettingsKind,
		`{"max_model_tool_steps":-1,"max_runtime_seconds":60,"max_consecutive_gate_failures":3}`, "hand-edited")
	code, _, stderr := runRecoveryCmd(t, "validate", "-config", configPath)
	if code == 0 {
		t.Fatal("validate accepted a stored resource the write path would refuse")
	}
	if !strings.Contains(stderr, controlplane.WorkflowExecutionSettingsKind) {
		t.Errorf("refusal must name the resource kind; stderr = %q", stderr)
	}
}

// rollback is the one operation that closes the documented gap: with the State
// Store stopped, it replays the value an earlier revision recorded through the
// ordinary replace, so no rollback RPC is needed and the rollback itself is
// audited as one more revision.
func TestRecoveryRollbackReplaysTheRevisionThroughReplace(t *testing.T) {
	configPath, st := recoveryFixture(t)
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"anthropic/claude"}`, "first")
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"openai/gpt"}`, "second")

	code, stdout, stderr := runRecoveryCmd(t, "rollback", "-config", configPath, "-kind", controlplane.ModelRoleAssignmentsKind)
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
	configPath, st := recoveryFixture(t)
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"anthropic/claude"}`, "first")
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"openai/gpt"}`, "second")
	seedResource(t, st, controlplane.ModelRoleAssignmentsKind, `{"implement":"google/gemini"}`, "third")

	code, _, stderr := runRecoveryCmd(t, "rollback", "-config", configPath, "-kind", controlplane.ModelRoleAssignmentsKind, "-revision", "1")
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
	newStore := func(t *testing.T, revisions int) (string, *pgstore.TaskDB) {
		t.Helper()
		configPath, st := recoveryFixture(t)
		for i := range revisions {
			value := `{"implement":"anthropic/claude"}`
			if i > 0 {
				value = `{"implement":"openai/gpt"}`
			}
			seedResource(t, st, controlplane.ModelRoleAssignmentsKind, value, "seed")
		}
		return configPath, st
	}

	t.Run("unknown_kind", func(t *testing.T) {
		configPath, _ := newStore(t, 2)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-config", configPath, "-kind", "not-a-resource")
		if code == 0 {
			t.Fatal("rollback of an unknown resource kind succeeded")
		}
		if !strings.Contains(stderr, "not-a-resource") {
			t.Errorf("refusal must name the kind; stderr = %q", stderr)
		}
	})

	t.Run("no_earlier_revision", func(t *testing.T) {
		configPath, _ := newStore(t, 1)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-config", configPath, "-kind", controlplane.ModelRoleAssignmentsKind)
		if code == 0 {
			t.Fatal("rollback with no earlier revision succeeded")
		}
		if !strings.Contains(stderr, "no earlier revision") {
			t.Errorf("refusal must say there is nothing to roll back to; stderr = %q", stderr)
		}
	})

	t.Run("current_revision", func(t *testing.T) {
		configPath, _ := newStore(t, 2)
		code, _, stderr := runRecoveryCmd(t, "rollback", "-config", configPath, "-kind", controlplane.ModelRoleAssignmentsKind, "-revision", "2")
		if code == 0 {
			t.Fatal("rollback of the current revision succeeded")
		}
		if !strings.Contains(stderr, "is the current version") {
			t.Errorf("refusal must say the revision is already the current value; stderr = %q", stderr)
		}
	})

	t.Run("live_state_store", func(t *testing.T) {
		configPath, st := newStore(t, 2)
		claim, err := postgres.AcquireOwnership(t.Context(), st.Pool, postgres.OwnerStateStore)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = claim.Release(t.Context()) })
		code, _, stderr := runRecoveryCmd(t, "rollback", "-config", configPath, "-kind", controlplane.ModelRoleAssignmentsKind)
		if code == 0 {
			t.Fatal("rollback against a live State Store succeeded")
		}
		if !strings.Contains(stderr, "stop the State Store") {
			t.Errorf("refusal must say to stop the State Store; stderr = %q", stderr)
		}
	})
}

// A positional argument that is not a subcommand used to be ignored, leaving
// an operator who mistyped a command watching a server start instead.
func TestRecoveryRejectsAnUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRecovery([]string{"backp", "-out", "x"}, &stdout, &stderr)
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
	url := pgtest.URL(t)
	pgstore.PoolAt(t, url)
	configPath := writeConfigFor(t, dir, url)
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
func seedStoreResources(t *testing.T, st *pgstore.TaskDB, configPath string) {
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
	_, skipped, err := server.ImportConfig(t.Context(), doc.Config)
	if err != nil {
		t.Fatalf("seed store resources: %v", err)
	}
	if len(skipped) > 0 {
		// This helper stands in for the State Store's own seeding, which seeds
		// every kind it can and leaves the rest absent. A skipped kind here
		// would mean the fixture store is missing a resource it claims to hold,
		// and the verdict under test would be about a different store.
		t.Fatalf("seed store resources: %d kind(s) refused: %+v", len(skipped), skipped)
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
func seedResource(t *testing.T, st *pgstore.TaskDB, kind, value, label string) {
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
	if _, err := st.PutResource(t.Context(), storecontract.ResourceWrite{
		Kind: kind, Value: encoded, Actor: label, Source: "test",
		RequestID: fmt.Sprintf("%s-%d", label, expected+1), ExpectedVersion: expected,
	}); err != nil {
		t.Fatalf("seed resource %s: %v", kind, err)
	}
}
