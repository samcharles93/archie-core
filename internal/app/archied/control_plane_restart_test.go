package archied

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/webui"
)

// restartAdminToken is the administrative credential the State Store's
// TaskGrants interceptor accepts. PutApplyStatus and ListApplyStatus are
// administrative (docs/prds/control-plane-apply-status.md): a task-scoped
// grant is denied by authorizesTaskScopedCall's deny-by-default arm, so the
// harness that reads and writes apply status must dial as the administrator.
const restartAdminToken = "eju6-admin-token"

// documentedStaleWindow is the window the operator-facing contract documents:
// every process re-stamps its records every 30 seconds, and "a record is stale
// once it is older than 90 seconds" (docs/prds/control-plane-apply-status.md,
// "What is reported"). It is a fixture literal on purpose. Reading
// applystatus.StaleAfter here would derive the aged row from the constant the
// reader is supposed to honour, so the fixture would move with any mutation of
// that constant and the reading below would agree with the reader by
// construction -- a test that passes for a window measured in hours is a test
// that never went stale at all.
const documentedStaleWindow = 90 * time.Second

// restartEditedSettings is the change the dashboard writes. It differs from
// fileConfig()'s [budgets] on all three fields, so a process that comes back on
// any file value is detectably not on the stored one.
var restartEditedSettings = workflow.ExecutionSettings{
	MaxModelToolSteps: 40, MaxRuntime: 10 * time.Minute, MaxConsecutiveGateFailures: 2, MaxTaskRuntime: 4 * time.Hour,
}

// controlPlaneRestartFixture is a real State Store for the restart test: the
// production control plane and apply-status surfaces registered on the real
// gRPC service, behind TaskGrants' admin/task-grant interceptors, over a real
// SQLite store. It is the composition state_store.go serves, not a stub.
type controlPlaneRestartFixture struct {
	listener *bufconn.Listener
	admin    *staterpc.Client
}

// newControlPlaneRestartFixture opens the store, seeds it the way
// archie-state-store does on a fresh database, serves the contract over
// bufconn, and returns an administrative client for the dashboard and for
// reading apply status back.
func newControlPlaneRestartFixture(t *testing.T) *controlPlaneRestartFixture {
	t.Helper()
	local := pgstore.Open(t)
	t.Cleanup(func() { _ = local.Close() })

	steps, err := stepVocabulary()
	if err != nil {
		t.Fatalf("build step vocabulary: %v", err)
	}
	control, err := controlplane.NewServer(local, steps)
	if err != nil {
		t.Fatalf("build control plane server: %v", err)
	}
	// The seed the State Store runs before it serves: every kind lands at
	// version 1 holding the file document's value. A kind the validator refused
	// would be absent, and the assertions below would then be testing a missing
	// resource rather than a restart, so this test names the two kinds it needs.
	versions, _, err := control.ImportConfig(t.Context(), fileConfig())
	if err != nil {
		t.Fatalf("seed control plane resources: %v", err)
	}
	for _, kind := range []string{controlplane.WorkflowExecutionSettingsKind, controlplane.PluginSettingsKind} {
		if versions[kind] != 1 {
			t.Fatalf("%s seeded at version %d, want 1", kind, versions[kind])
		}
	}

	grants := &staterpc.TaskGrants{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(grants.UnaryInterceptor(restartAdminToken)),
		grpc.ChainStreamInterceptor(grants.StreamInterceptor(restartAdminToken)),
	)
	staterpc.RegisterServer(server, staterpc.Deps{
		ControlPlane: control, Tasks: local, ApplyStatus: local, Grants: grants,
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	return &controlPlaneRestartFixture{
		listener: listener,
		admin:    dialStateStore(t, listener, restartAdminToken),
	}
}

// dial dials a fresh administrative connection, what a restarted process would
// open to the State Store it does not own.
func (f *controlPlaneRestartFixture) dial(t *testing.T) *staterpc.Client {
	t.Helper()
	return dialStateStore(t, f.listener, restartAdminToken)
}

// dialStateStore is the production client wiring (staterpc.Dial, the only dial
// path the daemon and dashboard use) against the non-loopback bridge address
// every container-mode profile uses, with the dialer redirected to bufconn.
func dialStateStore(t *testing.T, listener *bufconn.Listener, token string) *staterpc.Client {
	t.Helper()
	client, cleanup, err := staterpc.Dial("172.17.0.1:9090", token, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}))
	if err != nil {
		t.Fatalf("dial state store: %v", err)
	}
	t.Cleanup(cleanup)
	return client
}

// startRestartProcess composes the consuming half of archied the way run()
// composes it: boot.runtimeConfig layers the stored restart-required kinds over
// the file document, and the watched execution settings are applied and
// recorded from there. Apply status is reported through the State Store
// contract by the process's own reporter.
func startRestartProcess(ctx context.Context, t *testing.T, client *staterpc.Client) *boot {
	t.Helper()
	base := fileConfig()
	b := &boot{
		cfg:          base,
		log:          slog.New(slog.DiscardHandler),
		processName:  applystatus.Daemon,
		cfgHolder:    config.NewHolder(base),
		controlPlane: controlplane.NewRPCClient(client.ControlPlane()),
	}
	b.applyStatus = applystatus.New(b.processName, client, b.log)
	if err := b.loadRuntimeConfig(ctx); err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if err := b.startWorkflowExecutionSettings(ctx); err != nil {
		t.Fatalf("start workflow execution settings: %v", err)
	}
	return b
}

// TestRestartKeepsADatabaseOwnedSettingsChange is archie-core-eju6, the
// end-to-end restart test docs/prds/runtime-control-plane.md ("First
// implementation") lists as a deliverable. It drives settings changes through
// the dashboard's own control-plane handler, reads one back from the audit
// trail, restarts the process that consumes it, and asserts the restarted
// process is running the stored values rather than the file's -- across both
// halves of boot.runtimeConfig: the queried restart-required kinds and the
// watched execution budgets (archie-core-ju85).
//
// The restart is a fresh boot composition dialing a fresh connection to the
// State Store that holds the change. State Store persistence is what an OS
// restart preserves; re-creating the consumer is what an OS restart does to
// the consumer, so nothing in the property under test is substituted.
func TestRestartKeepsADatabaseOwnedSettingsChange(t *testing.T) {
	fixture := newControlPlaneRestartFixture(t)
	dashboard := &webui.Server{ControlPlane: fixture.admin.ControlPlane(), ApplyStatus: fixture.admin}

	// A process is already running on the seeded values. It is stopped before
	// the edit so the restart below -- not the live watch -- is what reads the
	// change back.
	firstCtx, stopFirst := context.WithCancel(t.Context())
	first := startRestartProcess(firstCtx, t, fixture.dial(t))
	stopFirst()
	if got := first.cfgHolder.Get().Budgets; got != budgetsFor(workflow.ExecutionSettings{MaxModelToolSteps: 1, MaxRuntime: time.Minute, MaxTaskRuntime: 4 * time.Hour}) {
		t.Fatalf("first process budgets = %+v, want the seeded file budgets", got)
	}
	if got := first.cfgHolder.Get().PluginDir; got != fileConfig().PluginDir {
		t.Fatalf("first process plugin dir = %q, want the file's", got)
	}

	// 1. The dashboard writes the change. Its handler, not this test, supplies
	// the attribution: webAudit attributes to identity.SystemID.
	expectedVersion := int64(1)
	executionSettingsDocument := []byte(`{"max_model_tool_steps":40,"max_runtime_seconds":600,"max_consecutive_gate_failures":2,"max_task_runtime_seconds":14400}`)
	if version := dashboardReplace(t, dashboard, controlplane.WorkflowExecutionSettingsKind, executionSettingsDocument, expectedVersion); version != 2 {
		t.Fatalf("workflow execution settings after edit = version %d, want 2", version)
	}
	pluginDocument := []byte(`{"plugin_dir":"/db/plugins","module_dir":"/db/modules","secret_engine_dir":"/db/secrets","skills_dir":"/db/skills"}`)
	if version := dashboardReplace(t, dashboard, controlplane.PluginSettingsKind, pluginDocument, expectedVersion); version != 2 {
		t.Fatalf("plugin settings after edit = version %d, want 2", version)
	}

	// 2. The change lands in the audit trail as an ordinary operator edit: the
	// newest revision names the dashboard and the System identity, and carries
	// the version it replaced.
	revisions := dashboardHistory(t, dashboard, controlplane.WorkflowExecutionSettingsKind)
	if len(revisions) != 2 {
		t.Fatalf("revisions = %d, want the seed and the edit", len(revisions))
	}
	edit := revisions[0]
	if edit.Version != 2 || edit.Actor != string(identity.SystemID) || edit.Source != "archie-ui" || edit.RequestID == "" {
		t.Fatalf("newest revision audit = %+v, want the dashboard's System-attributed edit at version 2", edit)
	}
	if revisions[1].Actor != "system:migration" || revisions[1].Version != 1 {
		t.Fatalf("older revision = %+v, want the migration seed it replaced", revisions[1])
	}
	// The value rides along, so a restore is an ordinary replace of it.
	if got := decodeExecutionSettingsRevision(t, edit.Value); got != restartEditedSettings {
		t.Fatalf("revision value = %+v, want the edited %+v", got, restartEditedSettings)
	}

	// 3-4. Restart the consumer. It re-resolves the same file document and
	// must come back on the stored values for both the queried kind and the
	// watched budgets.
	restartCtx, stopRestart := context.WithCancel(t.Context())
	defer stopRestart()
	restarted := startRestartProcess(restartCtx, t, fixture.dial(t))

	settings := restarted.executionSettings.Load()
	if settings == nil {
		t.Fatal("restarted process recorded no workflow execution settings")
	}
	if *settings != restartEditedSettings {
		t.Errorf("restarted settings = %+v, want the stored %+v", *settings, restartEditedSettings)
	}
	if got := restarted.cfgHolder.Get().Budgets; got != budgetsFor(restartEditedSettings) {
		t.Errorf("restarted budgets = %+v, want the stored %+v", got, budgetsFor(restartEditedSettings))
	}
	if got := restarted.cfgHolder.Get().PluginDir; got != "/db/plugins" {
		t.Errorf("restarted plugin dir = %q, want the stored /db/plugins, not the file's %q", got, fileConfig().PluginDir)
	}

	// The SIGHUP reload re-resolves the file document alone. It runs the same
	// boot.runtimeConfig, so it must not revert either half to its file value
	// (archie-core-ju85).
	if err := restarted.reloadConfig(restartCtx, &configuration.Document{Config: fileConfig()}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := restarted.cfgHolder.Get().Budgets; got != budgetsFor(restartEditedSettings) {
		t.Errorf("budgets after reload = %+v, want the stored %+v re-applied", got, budgetsFor(restartEditedSettings))
	}
	if got := restarted.cfgHolder.Get().PluginDir; got != "/db/plugins" {
		t.Errorf("plugin dir after reload = %q, want the stored /db/plugins", got)
	}

	// 5. The restarted process reports the version it applied, for both halves,
	// and a record that has stopped being re-stamped reads as not reporting.
	reported, err := fixture.admin.ListApplyStatus(t.Context())
	if err != nil {
		t.Fatalf("list apply status: %v", err)
	}
	for _, kind := range []string{controlplane.WorkflowExecutionSettingsKind, controlplane.PluginSettingsKind} {
		record := applyStatusFor(t, reported, applystatus.Daemon, kind)
		if record.AppliedVersion != 2 {
			t.Errorf("%s apply status version = %d, want the applied 2", kind, record.AppliedVersion)
		}
		if record.Error != "" {
			t.Errorf("%s apply status error = %q, want none for an applied version", kind, record.Error)
		}
	}

	statusView := dashboardApplyStatus(t, dashboard)
	live := applyStatusViewFor(t, statusView, applystatus.Daemon, controlplane.WorkflowExecutionSettingsKind)
	if live.State != "current" {
		t.Errorf("a record re-stamped at restart reads %q, want current", live.State)
	}

	// One record per (process, kind), replaced in place: an old report for the
	// same key replaces the live one rather than sitting beside it.
	stale := time.Now().Add(-documentedStaleWindow - time.Second)
	if err := fixture.admin.PutApplyStatus(t.Context(), storecontract.ApplyStatus{
		Process: applystatus.Daemon, Kind: controlplane.WorkflowExecutionSettingsKind,
		AppliedVersion: 2, ReportedAt: stale,
	}); err != nil {
		t.Fatalf("put stale apply status: %v", err)
	}
	statusView = dashboardApplyStatus(t, dashboard)
	aged := applyStatusViewFor(t, statusView, applystatus.Daemon, controlplane.WorkflowExecutionSettingsKind)
	if aged.State != "unknown" {
		t.Errorf("a record older than %v reads %q, want unknown rather than current", documentedStaleWindow, aged.State)
	}
	if count := countApplyStatusViews(statusView, applystatus.Daemon, controlplane.WorkflowExecutionSettingsKind); count != 1 {
		t.Errorf("rows for (%s, %s) = %d, want the report replaced in place", applystatus.Daemon, controlplane.WorkflowExecutionSettingsKind, count)
	}

	// The administrative credential is what admits the report: a task-scoped
	// grant, which the daemon issues to agent containers, is refused by both
	// apply-status RPCs.
	grant, err := fixture.admin.RegisterTaskGrant(t.Context(), 1, time.Hour)
	if err != nil {
		t.Fatalf("register task grant: %v", err)
	}
	taskScoped := dialStateStore(t, fixture.listener, grant)
	if _, err := taskScoped.ListApplyStatus(t.Context()); status.Code(err) != codes.PermissionDenied {
		t.Errorf("ListApplyStatus with a task-scoped grant = %v, want PermissionDenied", err)
	}
	if err := taskScoped.PutApplyStatus(t.Context(), storecontract.ApplyStatus{Process: applystatus.Daemon, Kind: controlplane.PluginSettingsKind, AppliedVersion: 9}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("PutApplyStatus with a task-scoped grant = %v, want PermissionDenied", err)
	}
}

type controlPlaneRevision struct {
	Version   int64           `json:"version"`
	Value     json.RawMessage `json:"value"`
	Actor     string          `json:"actor"`
	Source    string          `json:"source"`
	RequestID string          `json:"request_id"`
}

type applyStatusRecord struct {
	Process        string `json:"process"`
	Kind           string `json:"kind"`
	AppliedVersion int64  `json:"applied_version"`
	Error          string `json:"error"`
	State          string `json:"state"`
}

// decodeExecutionSettingsRevision reads a revision's value in the same shape
// the stored document uses, so the audit assertion compares values rather than
// just versions.
func decodeExecutionSettingsRevision(t *testing.T, value json.RawMessage) workflow.ExecutionSettings {
	t.Helper()
	var document struct {
		MaxModelToolSteps          int   `json:"max_model_tool_steps"`
		MaxRuntimeSeconds          int64 `json:"max_runtime_seconds"`
		MaxConsecutiveGateFailures int   `json:"max_consecutive_gate_failures"`
		MaxTaskRuntimeSeconds      int64 `json:"max_task_runtime_seconds"`
	}
	if err := json.Unmarshal(value, &document); err != nil {
		t.Fatalf("decode revision value: %v (%s)", err, value)
	}
	return workflow.ExecutionSettings{
		MaxModelToolSteps:          document.MaxModelToolSteps,
		MaxRuntime:                 time.Duration(document.MaxRuntimeSeconds) * time.Second,
		MaxConsecutiveGateFailures: document.MaxConsecutiveGateFailures,
		MaxTaskRuntime:             time.Duration(document.MaxTaskRuntimeSeconds) * time.Second,
	}
}

// dashboardReplace writes a resource the way the dashboard does -- through the
// HTTP handler, which supplies the operator attribution the request body never
// carries -- and returns the new version.
func dashboardReplace(t *testing.T, server *webui.Server, kind string, value []byte, expectedVersion int64) int64 {
	t.Helper()
	body, err := json.Marshal(map[string]any{"value": json.RawMessage(value), "expected_version": expectedVersion})
	if err != nil {
		t.Fatalf("encode %s request: %v", kind, err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/control-plane/resources/"+kind+"/commands/replace", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dashboard replace %s = %d, want 200: %s", kind, recorder.Code, recorder.Body.String())
	}
	var response struct {
		Resource struct {
			Version int64 `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode %s replace response: %v (%s)", kind, err, recorder.Body.String())
	}
	return response.Resource.Version
}

// dashboardHistory reads a resource's audit trail through the dashboard route,
// so the edit observed is the one the UI would show.
func dashboardHistory(t *testing.T, server *webui.Server, kind string) []controlPlaneRevision {
	t.Helper()
	var view struct {
		Revisions []controlPlaneRevision `json:"revisions"`
	}
	dashboardGET(t, server, "/api/control-plane/resources/"+kind+"/history", &view)
	return view.Revisions
}

// dashboardApplyStatus reads the apply-status page the dashboard renders.
func dashboardApplyStatus(t *testing.T, server *webui.Server) []applyStatusRecord {
	t.Helper()
	var view struct {
		Records []applyStatusRecord `json:"records"`
	}
	dashboardGET(t, server, "/api/control-plane/apply-status", &view)
	return view.Records
}

func dashboardGET(t *testing.T, server *webui.Server, path string, target any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, recorder.Body.String())
	}
}

func applyStatusFor(t *testing.T, statuses []storecontract.ApplyStatus, process, kind string) storecontract.ApplyStatus {
	t.Helper()
	for _, record := range statuses {
		if record.Process == process && record.Kind == kind {
			return record
		}
	}
	t.Fatalf("no apply status for (%s, %s) among %+v", process, kind, statuses)
	return storecontract.ApplyStatus{}
}

func applyStatusViewFor(t *testing.T, records []applyStatusRecord, process, kind string) applyStatusRecord {
	t.Helper()
	for _, record := range records {
		if record.Process == process && record.Kind == kind {
			return record
		}
	}
	t.Fatalf("no apply-status row for (%s, %s) among %+v", process, kind, records)
	return applyStatusRecord{}
}

func countApplyStatusViews(records []applyStatusRecord, process, kind string) int {
	count := 0
	for _, record := range records {
		if record.Process == process && record.Kind == kind {
			count++
		}
	}
	return count
}
