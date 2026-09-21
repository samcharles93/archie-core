package archied

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// The task limits this test moves between the file document and the database.
// Every field differs between the two, so a process that came back on the
// file's value is distinguishable from one that came back on the stored one.
var (
	restartFileLimits   = workflow.ExecutionSettings{MaxModelToolSteps: 10, MaxRuntime: 5 * time.Minute, MaxConsecutiveGateFailures: 3}
	restartStoredLimits = workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4}
)

// restartStoredDocument is what the dashboard's editor sends for
// restartStoredLimits: the resource's own document shape, spelled the way the
// control plane's executionSettingsDocument spells it.
const restartStoredDocument = `{"max_model_tool_steps": 25, "max_runtime_seconds": 3600, "max_consecutive_gate_failures": 4}`

// settingsDocument mirrors the stored document, so an assertion can read a
// revision's value back as the limits it means rather than as bytes.
type settingsDocument struct {
	MaxModelToolSteps          int   `json:"max_model_tool_steps"`
	MaxRuntimeSeconds          int64 `json:"max_runtime_seconds"`
	MaxConsecutiveGateFailures int   `json:"max_consecutive_gate_failures"`
}

func (d settingsDocument) limits() workflow.ExecutionSettings {
	return workflow.ExecutionSettings{
		MaxModelToolSteps:          d.MaxModelToolSteps,
		MaxRuntime:                 time.Duration(d.MaxRuntimeSeconds) * time.Second,
		MaxConsecutiveGateFailures: d.MaxConsecutiveGateFailures,
	}
}

// TestControlPlaneSettingsChangeSurvivesProcessRestart is archie-core-eju6: the
// end-to-end restart check docs/prds/runtime-control-plane.md ("First
// implementation") lists as a deliverable. In one test it writes a task-limit
// change the way the dashboard writes one, sees the audit trail record it as an
// ordinary edit, stops the process that consumes it, and starts it again --
// against the same State Store and the same file document -- and asserts the
// restarted process is running the stored limits rather than the file's, that
// the apply-status row for it reports the version it applied, and that a record
// older than the staleness window reads as not reporting rather than as current.
//
// Everything below the assertions is the production path: the State Store's own
// control-plane composition over the store it owns, seeded from the same file
// document the daemon reads (ImportConfig), served on the token-protected
// topology every deployment profile uses; the dashboard's HTTP adapter, whose
// attribution the test never supplies itself; and archied's own boot sequence.
func TestControlPlaneSettingsChangeSurvivesProcessRestart(t *testing.T) {
	const adminToken = "state-store-admin-token"
	kind := controlplane.WorkflowExecutionSettingsKind

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	// The listener comes first so the file document can name the endpoint every
	// process here dials: one document, read by the State Store and by both
	// boots of the daemon.
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	target := listener.Addr().String()
	writeConfig(t, cfgPath, restartConfigTOML(dir, target, adminToken))

	dashboard, admin := serveRestartControlPlane(t, listener, cfgPath, target, adminToken)

	// The process that consumes task limits is already running when the
	// operator edits, on the limits the file document carries, and it has
	// reported the version it applied.
	first, stopFirst := bootRestartProcess(t, cfgPath)
	if got, want := first.cfgHolder.Get().Budgets, budgetsFor(restartFileLimits); got != want {
		t.Fatalf("running budgets before the edit = %+v, want the file document's %+v", got, want)
	}
	if row := reportedRow(t, admin, kind); row.AppliedVersion != 1 || row.Error != "" {
		t.Fatalf("apply status before the edit = %+v, want version 1 and no error", row)
	}

	// 1. The dashboard writes the change. The actor, source and request ID are
	// the adapter's (webui's webAudit), not this test's: a request body cannot
	// claim them.
	replaceThroughDashboard(t, dashboard, kind, restartStoredDocument, 1)

	// 2. The audit trail carries it as an ordinary edit: the operator's
	// identity, the dashboard as its source, and the value that was written.
	revisions := historyThroughDashboard(t, dashboard, kind)
	if len(revisions) != 2 {
		t.Fatalf("audit trail holds %d revisions, want the migration seed and the edit", len(revisions))
	}
	if edit := revisions[0]; edit.Version != 2 || edit.Actor != string(identity.SystemID) || edit.Source != "archie-ui" || !strings.HasPrefix(edit.RequestID, "ui-") {
		t.Fatalf("the edit was recorded as %+v, want an ordinary archie-ui edit by the System identity at version 2", edit)
	}
	if got, err := documentLimits(revisions[0].Value); err != nil || got != restartStoredLimits {
		t.Fatalf("the audit trail recorded %s, want %+v: %v", revisions[0].Value, restartStoredLimits, err)
	}
	// The revision under it is the migration seed. Without it the row above
	// would be indistinguishable from a resource that had only ever been
	// written by the importer.
	if seed := revisions[1]; seed.Actor != "system:migration" || seed.Source != "legacy-config" {
		t.Fatalf("the first revision = %+v, want the legacy-config migration seed", seed)
	}

	// 3. The process stops, and comes back against the same State Store and the
	// same file document.
	stopFirst()
	restarted, stopRestarted := bootRestartProcess(t, cfgPath)
	defer stopRestarted()

	// 4. It came back on the stored value, not the file's.
	if got, want := restarted.cfgHolder.Get().Budgets, budgetsFor(restartStoredLimits); got != want {
		t.Fatalf("running budgets after the restart = %+v, want the stored %+v (the file document still says %+v)", got, want, budgetsFor(restartFileLimits))
	}
	if got := restarted.executionSettings.Load(); got == nil || *got != restartStoredLimits {
		t.Fatalf("the restarted process records %v as the settings it applied, want %+v", got, restartStoredLimits)
	}

	// 5. The apply-status row for the restarted process reports the version it
	// applied, and the settings page reads that row as current...
	if row := reportedRow(t, admin, kind); row.AppliedVersion != 2 || row.Error != "" {
		t.Fatalf("apply status after the restart = %+v, want version 2 and no error", row)
	}
	if state := applyStatusState(t, dashboard, kind); state != applyStateCurrentWire {
		t.Fatalf("the settings page reads the freshly re-stamped row as %q, want %q", state, applyStateCurrentWire)
	}

	// ...and reads a row older than the staleness window as not reporting
	// rather than as current. The row is re-stamped every 30 seconds by the
	// process that wrote it, so a row that stopped being re-stamped is a
	// process that stopped -- the reader must not present its last report as
	// live. Ageing it through the same administrative surface is what the
	// clock would have done to it.
	if err := admin.PutApplyStatus(t.Context(), storecontract.ApplyStatus{
		Process: applystatus.Daemon, Kind: kind, AppliedVersion: 2,
		ReportedAt: time.Now().UTC().Add(-applystatus.StaleAfter - time.Second),
	}); err != nil {
		t.Fatalf("age the apply status row: %v", err)
	}
	if state := applyStatusState(t, dashboard, kind); state != applyStateNotReportingWire {
		t.Fatalf("the settings page reads the aged row as %q, want %q (not reporting, never current)", state, applyStateNotReportingWire)
	}
}

// The two readings the dashboard renders for a row it holds. They are the
// wire's values, not this package's: webui's applyState returns them and
// ui/src/settings/ApplyStatusRows.vue shows "Not reporting" for the second, so
// asserting the wire value asserts what the operator is shown.
const (
	applyStateCurrentWire      = "current"
	applyStateNotReportingWire = "unknown"
)

// restartConfigTOML is the operator's file document: the bootstrap settings a
// process reads before it can reach the State Store, the task limits the file
// still carries (the values the database replaces), the container image
// configuration.Validate requires, and the State Store endpoint.
func restartConfigTOML(dir, target, adminToken string) string {
	return fmt.Sprintf(`bot_user = "widget"
db_path = %q

[forge]
type = "github"
host = "https://github.example.com"

[[repos]]
owner = "acme"
name = "app"

[containers]
image = "archie:test"
pull_policy = "missing"

[budgets]
max_steps = %d
wall_clock = %q
gate_max_failures = %d

[services.state]
target = %q
target_token = %q
`, filepath.Join(dir, "archie"), restartFileLimits.MaxModelToolSteps, restartFileLimits.MaxRuntime,
		restartFileLimits.MaxConsecutiveGateFailures, target, adminToken)
}

// serveRestartControlPlane serves the State Store contract the way the
// standalone process serves it -- the production control-plane composition
// (openStateStoreControlPlane) over the store the process owns, seeded from the
// file document by the same ImportConfig call RunStateStore makes -- and
// returns the dashboard's own view of it plus the administratively dialed
// client.
//
// The credential is the administrative token, the only one PutApplyStatus and
// ListApplyStatus admit: a task-scoped grant is refused both of them by the
// deny-by-default arm of authorizesTaskScopedCall. The listener is the token
// interceptor pair stateStoreServerOpts installs for the non-loopback topology
// every container-mode deployment uses; the socket stays on loopback because
// this deployment is one host and the credential is what the harness needs.
func serveRestartControlPlane(t *testing.T, listener net.Listener, cfgPath, target, adminToken string) (*webui.Server, *staterpc.Client) {
	t.Helper()
	ctx := t.Context()

	st, err := store.Open(ctx, filepath.Join(filepath.Dir(cfgPath), "archie.db-tasks.sqlite"))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	storeBoot := newBootstrap()
	storeBoot.stderrLog = true
	storeBoot.log = slog.New(slog.DiscardHandler)
	if err := storeBoot.loadConfig(ctx, cfgPath, ""); err != nil {
		t.Fatalf("state store load config: %v", err)
	}

	control, err := openStateStoreControlPlane(st)
	if err != nil {
		t.Fatalf("build the control plane server: %v", err)
	}
	versions, skipped, err := control.ImportConfig(ctx, storeBoot.cfg)
	if err != nil {
		t.Fatalf("seed control-plane resources: %v", err)
	}
	for _, skip := range skipped {
		// Not fatal, by design: a kind the file config cannot seed stays absent
		// and the file's value stays in effect. Reported so a kind this test
		// cares about cannot be skipped silently.
		t.Logf("control-plane resource not seeded from the file document: %s: %v", skip.Kind, skip.Err)
	}
	if got := versions[controlplane.WorkflowExecutionSettingsKind]; got != 1 {
		t.Fatalf("the file document seeded workflow execution settings at version %d, want 1", got)
	}

	grants := &staterpc.TaskGrants{}
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(grants.UnaryInterceptor(adminToken)),
		grpc.ChainStreamInterceptor(grants.StreamInterceptor(adminToken)),
	}
	storeBoot.st = st
	deps := storeBoot.stateStoreDeps(grants)
	deps.ControlPlane = control
	go func() { _ = serveStateStore(ctx, listener, deps, opts) }()

	admin, cleanup, err := staterpc.Dial(target, adminToken)
	if err != nil {
		t.Fatalf("dial the state store: %v", err)
	}
	t.Cleanup(cleanup)
	return &webui.Server{ControlPlane: admin.ControlPlane(), ApplyStatus: admin}, admin
}

// bootRestartProcess boots one archied process against the State Store at
// cfgPath and returns it with the stop func that ends it. The phases are Run's,
// in Run's order, for the surfaces this feature runs through: resolve the file
// document, dial the State Store surfaces, layer the restart-required kinds
// over that document, then apply the settings that arrive on the watch and
// record what it applied.
//
// Run's later phases (forge, containers, NATS, chat) consume no task limit, so
// they stay out: this is the process the feature runs in, not a second
// composition. A stub would not do -- the point of the test is that the value
// survives a real boot reading a real store.
func bootRestartProcess(t *testing.T, cfgPath string) (*boot, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	b := newBootstrap()
	// The daemon's log destination is a deployment file this test does not
	// create, so this boot logs where every other test's boot does.
	b.stderrLog = true
	b.log = slog.New(slog.DiscardHandler)
	// The process name is what an apply-status record carries; a boot
	// reporting under any other name would be invisible to the settings page.
	b.processName = applystatus.Daemon
	if err := b.loadConfig(ctx, cfgPath, ""); err != nil {
		cancel()
		t.Fatalf("load config: %v", err)
	}
	// openStores' own secret registry is not part of this feature: the file
	// document carries the State Store token explicitly, so the dial resolves
	// its credential without a forge credential this harness has no use for.
	b.secrets = &secret.Registry{}
	if err := b.openDaemonStateSurfaces(); err != nil {
		cancel()
		t.Fatalf("open the State Store surfaces: %v", err)
	}
	if err := b.loadRuntimeConfig(ctx); err != nil {
		cancel()
		t.Fatalf("layer the database settings over the file document: %v", err)
	}
	if err := b.startWorkflowExecutionSettings(ctx); err != nil {
		cancel()
		t.Fatalf("apply the stored workflow execution settings: %v", err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			// Cancel first, then release: a watch stream that fails on a live
			// context is reported as a failed apply, and this process is being
			// shut down, not failing.
			cancel()
			b.cleanup()
		})
	}
	t.Cleanup(stop)
	return b, stop
}

// documentLimits reads a stored or recorded document back as the limits it
// means.
func documentLimits(value []byte) (workflow.ExecutionSettings, error) {
	var document settingsDocument
	if err := json.Unmarshal(value, &document); err != nil {
		return workflow.ExecutionSettings{}, err
	}
	return document.limits(), nil
}

// dashboardRequest runs one request against the dashboard's own HTTP adapter.
func dashboardRequest(t *testing.T, dashboard *webui.Server, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	dashboard.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s = %d: %s", request.Method, request.URL.Path, recorder.Code, recorder.Body.String())
	}
	return recorder
}

// replaceThroughDashboard writes document as the dashboard's editor does, over
// the replace command, against the version the editor read. The attribution is
// the adapter's: the request body carries only the value and the version.
func replaceThroughDashboard(t *testing.T, dashboard *webui.Server, kind, document string, expectedVersion int64) {
	t.Helper()
	body := fmt.Sprintf(`{"value": %s, "expected_version": %d}`, document, expectedVersion)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/control-plane/resources/"+kind+"/commands/replace", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Archie-CSRF", "1")
	dashboardRequest(t, dashboard, request)
}

// revision is one row of the audit trail as the dashboard's history route
// renders it.
type revision struct {
	Version   int64           `json:"version"`
	Value     json.RawMessage `json:"value"`
	Actor     string          `json:"actor"`
	Source    string          `json:"source"`
	RequestID string          `json:"request_id"`
}

func historyThroughDashboard(t *testing.T, dashboard *webui.Server, kind string) []revision {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/control-plane/resources/"+kind+"/history", nil)
	recorder := dashboardRequest(t, dashboard, request)
	var response struct {
		Revisions []revision `json:"revisions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode the audit trail: %v (body %s)", err, recorder.Body.String())
	}
	if len(response.Revisions) == 0 {
		t.Fatalf("the audit trail for %s is empty", kind)
	}
	return response.Revisions
}

// reportedRow is the apply-status row the settings page shows for the daemon
// and kind. It is read through the administrative token, which is the only
// credential that reaches ListApplyStatus.
func reportedRow(t *testing.T, admin *staterpc.Client, kind string) storecontract.ApplyStatus {
	t.Helper()
	rows, err := admin.ListApplyStatus(t.Context())
	if err != nil {
		t.Fatalf("list apply status: %v", err)
	}
	for _, row := range rows {
		if row.Process == applystatus.Daemon && row.Kind == kind {
			return row
		}
	}
	t.Fatalf("no apply-status row for %s/%s in %+v", applystatus.Daemon, kind, rows)
	return storecontract.ApplyStatus{}
}

// applyStatusState is the reading the settings page renders for the daemon's
// row for kind.
func applyStatusState(t *testing.T, dashboard *webui.Server, kind string) string {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/control-plane/apply-status", nil)
	recorder := dashboardRequest(t, dashboard, request)
	var page struct {
		Records []struct {
			Process string `json:"process"`
			Kind    string `json:"kind"`
			State   string `json:"state"`
		} `json:"records"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode the apply-status page: %v (body %s)", err, recorder.Body.String())
	}
	for _, record := range page.Records {
		if record.Process == applystatus.Daemon && record.Kind == kind {
			return record.State
		}
	}
	t.Fatalf("the apply-status page carries no row for %s/%s: %+v", applystatus.Daemon, kind, page.Records)
	return ""
}
