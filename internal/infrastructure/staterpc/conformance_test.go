package staterpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/store"
)

// contract is the union of every store surface staterpc fronts (minus the
// playbook-dispatch ledger, split out below to keep the interface under the
// repo's 8-method cap), so the same test battery drives both the local
// *store.Store and the remote *Client through one interface value, per
// docs/prds/state-store-contract.md §11's conformance requirement.
type contract interface {
	store.TaskStore
	store.BindingTaskCreator
	store.ConfigSnapshotStore
	store.ApplyStatusStore
}

// edaContract is the event-capture half, which the PocketBase store serves.
// It is a separate interface because no single type implements both halves
// any more: that split is the point of the migration, not an accident.
type edaContract interface {
	store.CaptureStore
	store.MappingStore
	store.BindingStore
	store.BindingDispatcher
}

// playbookContract is the playbook-dispatch idempotency-ledger surface, split
// out from contract so each aggregate stays under the repo's interfacebloat
// cap without weakening what the conformance battery proves.
type playbookContract interface {
	store.PlaybookDispatcher
}

// taskLogContract is the task-log read group, driven separately because it is
// not a *store.Store surface: the reader is internal/logging's own registry,
// which owns the log format, so the local side of this contract is a registry
// rather than a store.
type taskLogContract interface {
	store.TaskLogStore
}

// remoteEDA fronts both stores for a battery that exercises event-capture
// surfaces over the wire.
func remoteEDA(t *testing.T, local *store.Store, eda *edastore.Store) *Client {
	t.Helper()
	return remoteTaskStore(t, local, nil, eda)
}

func remoteContract(t *testing.T, local *store.Store) contract {
	t.Helper()
	return remoteTaskStore(t, local, nil, nil)
}

// remoteTaskStore serves local behind a bufconn listener with a task-log
// reader attached, and returns a client for it. The reader is a parameter
// because the log files are internal/logging's, not the store's: this contract
// fronts a reader rather than a table.
// remoteTaskStore fronts the same two stores the real composition wires: the
// task store for the task surfaces, the event-capture store for captures,
// mappings, bindings and the dispatch ledgers. eda may be nil for a battery
// that exercises only task surfaces.
func remoteTaskStore(t *testing.T, local *store.Store, logs store.TaskLogStore, eda *edastore.Store) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	deps := Deps{
		Tasks: local, BindingTaskCreator: local,
		ConfigSnapshots: local, ApplyStatus: local,
		TaskLogs: logs,
	}
	if eda != nil {
		deps.Captures = eda
		deps.Mappings = eda
		deps.Bindings = eda
		deps.BindingDispatcher = eda
		deps.PlaybookDispatcher = eda
	}
	RegisterServer(server, deps)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewClient(conn)
}

// TestStateStoreConformance runs the same battery of contract calls through
// both the local adapter and a bufconn-backed gRPC client, asserting
// identical behaviour including error-sentinel fidelity and the
// not-found-as-(nil,nil) convention (§7, §11).
func TestStateStoreConformance(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			local := store.OpenTest(t)
			eda := edastore.OpenTest(t)
			var c contract = local
			var ec edaContract = eda
			var pc playbookContract = eda
			if mode == "grpc" {
				remote := remoteTaskStore(t, local, nil, eda)
				c = remote
				ec = remote
				pc = remote
			}

			// workflow.Store view: EnqueueChatTask, Transition, Update, InsertEvent.
			task, err := c.EnqueueChatTask(ctx, "acme", "widget", "title", "body", "implement", "")
			if err != nil || task == nil {
				t.Fatalf("EnqueueChatTask: %+v %v", task, err)
			}
			if err := c.Transition(ctx, task.ID, task.Status, "running", "started"); err != nil {
				t.Fatalf("Transition: %v", err)
			}
			task.Plan = "the plan"
			task.Status = "running"
			task.PRNumber = 401
			// The workflow pin is what a retry re-reads instead of resolving the
			// active control-plane resource again (docs/prds/runtime-control-plane.md:95).
			// A wire hop that drops it leaves the daemon's digest guard dead, so
			// the battery carries it through Update and back out of TaskByID.
			task.WorkflowDefinitionVersion = 7
			task.WorkflowDefinitionDigest = "sha256:beef"
			task.WorkflowDefinitionYAML = "name: implement\nsteps: []\n"
			if err := c.Update(ctx, task); err != nil {
				t.Fatalf("Update: %v", err)
			}
			eventID, err := c.InsertEvent(ctx, events.Event{Kind: "stage_finish", TaskID: task.ID, Attempt: 2, Data: map[string]any{"duration_ms": 12.0}})
			if err != nil || eventID == 0 {
				t.Fatalf("InsertEvent: %v %v", eventID, err)
			}
			// R4's wire acceptance: the per-attempt configuration document IS an
			// event, so it has to survive the same hop an event does, including
			// its nested structure and the attempt key that attributes it to one
			// run. A payload that arrives flattened reads as a config with no
			// fields at all. The document rides under the daemon's own key
			// (internal/daemon/daemon.go), asserted by name below.
			_, err = c.InsertEvent(ctx, events.Event{
				Kind: events.KindConfigCaptured, TaskID: task.ID, Attempt: 2,
				Data: map[string]any{
					"schema":   events.ConfigCapturedSchema,
					"document": map[string]any{"bot_user": "archie", "models": map[string]any{"implement": "anthropic/claude"}},
				},
			})
			if err != nil {
				t.Fatalf("InsertEvent config_captured: %v", err)
			}

			// Stale transition: from no longer matches current status.
			err = c.Transition(ctx, task.ID, "queued", "merged", "")
			if !errors.Is(err, store.ErrStaleTransition) {
				t.Fatalf("Transition stale = %v, want ErrStaleTransition", err)
			}

			// Remediation starter: the guarded transition carries the review
			// unit, and both guards are the wire contract (the sentinel must
			// survive the hop for the reaction consumer's dedup to work).
			if err := c.Transition(ctx, task.ID, "running", "pr_open", "PR opened"); err != nil {
				t.Fatalf("Transition to pr_open: %v", err)
			}
			if err := c.BeginRemediation(ctx, task.ID, `{"review_id":7}`); err != nil {
				t.Fatalf("BeginRemediation: %v", err)
			}
			if err := c.BeginRemediation(ctx, task.ID, `{"review_id":7}`); !errors.Is(err, store.ErrStaleTransition) {
				t.Fatalf("BeginRemediation stale = %v, want ErrStaleTransition", err)
			}
			remediated, err := c.TaskByID(ctx, task.ID)
			if err != nil || remediated == nil {
				t.Fatalf("TaskByID after BeginRemediation: %+v %v", remediated, err)
			}
			if remediated.Status != "queued" || remediated.Workflow != "remediate" || remediated.ReviewPayload != `{"review_id":7}` {
				t.Fatalf("BeginRemediation row = status:%q workflow:%q payload:%q", remediated.Status, remediated.Workflow, remediated.ReviewPayload)
			}
			if err := c.UpdateReviewPayload(ctx, task.ID, `{"review_id":7,"comments":[{"comment_id":9}]}`); err != nil {
				t.Fatalf("UpdateReviewPayload: %v", err)
			}
			if err := c.Transition(ctx, task.ID, "queued", "running", "claimed"); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if err := c.UpdateReviewPayload(ctx, task.ID, `{}`); !errors.Is(err, store.ErrStaleTransition) {
				t.Fatalf("UpdateReviewPayload stale = %v, want ErrStaleTransition", err)
			}

			// TaskQueries: found=false is not an error.
			missing, err := c.TaskByID(ctx, task.ID+999999)
			if err != nil || missing != nil {
				t.Fatalf("TaskByID missing: %+v %v", missing, err)
			}
			got, err := c.TaskByID(ctx, task.ID)
			if err != nil || got == nil || got.Plan != "the plan" {
				t.Fatalf("TaskByID: %+v %v", got, err)
			}
			if got.WorkflowDefinitionVersion != 7 || got.WorkflowDefinitionDigest != "sha256:beef" ||
				got.WorkflowDefinitionYAML != "name: implement\nsteps: []\n" {
				t.Errorf("workflow pin after round trip = (%d, %q, %q), want (7, \"sha256:beef\", the YAML); a dropped pin makes each retry re-resolve the active definition",
					got.WorkflowDefinitionVersion, got.WorkflowDefinitionDigest, got.WorkflowDefinitionYAML)
			}
			byIssue, err := c.TaskByIssue(ctx, task.Owner, task.Repo, task.IssueNumber)
			if err != nil || byIssue == nil || byIssue.ID != task.ID {
				t.Fatalf("TaskByIssue: %+v %v", byIssue, err)
			}
			if err := c.Transition(ctx, task.ID, "running", "pr_open", "PR #401"); err != nil {
				t.Fatalf("Transition to pr_open: %v", err)
			}
			byPR, err := c.OpenTaskByPR(ctx, task.Owner, task.Repo, task.PRNumber)
			if err != nil || byPR == nil || byPR.ID != task.ID {
				t.Fatalf("OpenTaskByPR: %+v %v", byPR, err)
			}
			missing, err = c.OpenTaskByPR(ctx, task.Owner, task.Repo, task.PRNumber+1)
			if err != nil || missing != nil {
				t.Fatalf("OpenTaskByPR missing: %+v %v", missing, err)
			}
			if _, err := c.Tasks(ctx, 10); err != nil {
				t.Fatalf("Tasks: %v", err)
			}
			if _, err := c.StatusCounts(ctx); err != nil {
				t.Fatalf("StatusCounts: %v", err)
			}
			if _, err := c.OpenPRs(ctx); err != nil {
				t.Fatalf("OpenPRs: %v", err)
			}

			// TaskEvents / EventsSince / stats.
			evs, err := c.TaskEvents(ctx, task.ID)
			if err != nil || len(evs) != 2 || evs[0].Data["duration_ms"] != 12.0 {
				t.Fatalf("TaskEvents: %+v %v", evs, err)
			}
			if evs[0].Attempt != 2 {
				t.Errorf("TaskEvents attempt = %d, want 2 (provenance must survive the wire)", evs[0].Attempt)
			}
			if evs[1].Kind != events.KindConfigCaptured || evs[1].Attempt != 2 || evs[1].Data["schema"] != events.ConfigCapturedSchema {
				t.Errorf("config_captured event = %+v, want its kind, attempt and schema preserved", evs[1])
			}
			if _, renamed := evs[1].Data["config"]; renamed {
				t.Error(`the document crossed under "config"; the producer writes it under "document"`)
			}
			if len(evs[1].Data) != 2 {
				t.Errorf("config_captured data keys = %v, want exactly schema and document", evs[1].Data)
			}
			doc, ok := evs[1].Data["document"].(map[string]any)
			if !ok || doc["bot_user"] != "archie" {
				t.Errorf("config_captured payload = %#v, want the decoded document under its own key", evs[1].Data)
			}
			if _, err := c.EventsSince(ctx, "", 10); err != nil {
				t.Fatalf("EventsSince: %v", err)
			}
			if _, err := c.WorkflowStats(ctx); err != nil {
				t.Fatalf("WorkflowStats: %v", err)
			}
			if _, err := c.StageStats(ctx); err != nil {
				t.Fatalf("StageStats: %v", err)
			}
			if _, err := c.TokensByDay(ctx, 7); err != nil {
				t.Fatalf("TokensByDay: %v", err)
			}

			// Lifecycle: ClaimNext / ClaimByIssue / Requeue / RetryTask / RecoverStale.
			if _, err := c.EnqueueIssue(ctx, "acme", "widget", 42, "issue title", "body", "bug", ""); err != nil {
				t.Fatalf("EnqueueIssue: %v", err)
			}
			claimed, err := c.ClaimNext(ctx)
			if err != nil || claimed == nil {
				t.Fatalf("ClaimNext: %+v %v", claimed, err)
			}
			if err := c.Requeue(ctx, claimed.ID, "running", ""); err != nil {
				t.Fatalf("Requeue: %v", err)
			}
			if err := c.RetryTask(ctx, claimed.ID, "queued", ""); err != nil {
				t.Fatalf("RetryTask: %v", err)
			}
			if _, err := c.RecoverStale(ctx); err != nil {
				t.Fatalf("RecoverStale: %v", err)
			}

			// Classified park: the guarded transition carries the park class,
			// and both guards are the wire contract (the stale sentinel must
			// survive the hop for the daemon's park-with-retry loop to detect
			// a concurrent terminal write).
			if err := c.Transition(ctx, claimed.ID, "queued", "running", "claimed"); err != nil {
				t.Fatalf("re-claim for ParkTask: %v", err)
			}
			if err := c.ParkTask(ctx, claimed.ID, "running", "container pool unavailable", "transient"); err != nil {
				t.Fatalf("ParkTask: %v", err)
			}
			parked, err := c.TaskByID(ctx, claimed.ID)
			if err != nil || parked == nil {
				t.Fatalf("TaskByID after ParkTask: %+v %v", parked, err)
			}
			if parked.Status != "parked" || parked.ParkClass != "transient" || parked.ParkReason != "container pool unavailable" {
				t.Fatalf("ParkTask row = status:%q class:%q reason:%q", parked.Status, parked.ParkClass, parked.ParkReason)
			}
			if err := c.ParkTask(ctx, claimed.ID, "running", "late park", "transient"); !errors.Is(err, store.ErrStaleTransition) {
				t.Fatalf("ParkTask stale = %v, want ErrStaleTransition", err)
			}
			// An unclassified park normalizes to needs_human store-side rather
			// than failing: a caller that predates the class vocabulary must
			// not be unable to park.
			if err := c.Requeue(ctx, claimed.ID, "parked", ""); err != nil {
				t.Fatalf("Requeue after park: %v", err)
			}
			if err := c.Transition(ctx, claimed.ID, "queued", "running", "claimed"); err != nil {
				t.Fatalf("re-claim 2: %v", err)
			}
			if err := c.ParkTask(ctx, claimed.ID, "running", "unclassified park", ""); err != nil {
				t.Fatalf("ParkTask unclassified: %v", err)
			}
			parked2, err := c.TaskByID(ctx, claimed.ID)
			if err != nil || parked2 == nil {
				t.Fatalf("TaskByID after unclassified park: %+v %v", parked2, err)
			}
			if parked2.ParkClass != "needs_human" {
				t.Fatalf("unclassified park class = %q, want needs_human", parked2.ParkClass)
			}
			byClaim, err := c.ClaimByIssue(ctx, "acme", "widget", 999999)
			if err != nil || byClaim != nil {
				t.Fatalf("ClaimByIssue missing: %+v %v", byClaim, err)
			}

			// Archive. RecoverStale above may have requeued task if it was
			// still running, so re-fetch its current status rather than
			// trusting the in-memory snapshot.
			current, err := c.TaskByID(ctx, task.ID)
			if err != nil || current == nil {
				t.Fatalf("TaskByID before archive: %+v %v", current, err)
			}
			if _, err := c.ArchiveTask(ctx, task.ID, current.Status, events.Event{Kind: "archived"}); err != nil {
				t.Fatalf("ArchiveTask: %v", err)
			}
			if _, err := c.ClearTerminalTasks(ctx); err != nil {
				t.Fatalf("ClearTerminalTasks: %v", err)
			}

			// Mapping: found=false, ErrMappingNotFound.
			mappingID, err := ec.InsertMapping(ctx, mapping.Mapping{Name: "m1", Fields: []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}}})
			if err != nil || mappingID == "" {
				t.Fatalf("InsertMapping: %v %v", mappingID, err)
			}
			missingMapping, err := ec.GetMapping(ctx, "rabsent00000000")
			if err != nil || missingMapping != nil {
				t.Fatalf("GetMapping missing: %+v %v", missingMapping, err)
			}
			gotMapping, err := ec.GetMapping(ctx, mappingID)
			if err != nil || gotMapping == nil || gotMapping.Name != "m1" {
				t.Fatalf("GetMapping: %+v %v", gotMapping, err)
			}
			if _, err := ec.ListMappings(ctx); err != nil {
				t.Fatalf("ListMappings: %v", err)
			}
			gotMapping.Name = "m1-renamed"
			if err := ec.UpdateMapping(ctx, *gotMapping); err != nil {
				t.Fatalf("UpdateMapping: %v", err)
			}
			err = ec.UpdateMapping(ctx, mapping.Mapping{ID: "rabsent00000000", Name: "x", Fields: gotMapping.Fields})
			if !errors.Is(err, store.ErrMappingNotFound) {
				t.Fatalf("UpdateMapping missing = %v, want ErrMappingNotFound", err)
			}
			err = ec.DeleteMapping(ctx, "rabsent00000000")
			if !errors.Is(err, store.ErrMappingNotFound) {
				t.Fatalf("DeleteMapping missing = %v, want ErrMappingNotFound", err)
			}

			// Binding: found=false, ErrBindingNotFound, ErrBindingOverlap,
			// ErrBindingTransition, and the dispatch surface incl.
			// ErrAlreadyDispatched.
			bindingID, err := ec.InsertBinding(ctx, binding.Binding{
				Name: "b1", Matcher: binding.Matcher{Source: "sentry"}, MappingID: mappingID,
				Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
			})
			if err != nil || bindingID == "" {
				t.Fatalf("InsertBinding: %v %v", bindingID, err)
			}
			_, err = ec.InsertBinding(ctx, binding.Binding{
				Name: "b2", Matcher: binding.Matcher{Source: "sentry"}, MappingID: mappingID,
				Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
			})
			if !errors.Is(err, store.ErrBindingOverlap) {
				t.Fatalf("InsertBinding overlap = %v, want ErrBindingOverlap", err)
			}
			missingBinding, err := ec.GetBinding(ctx, "rabsent00000000")
			if err != nil || missingBinding != nil {
				t.Fatalf("GetBinding missing: %+v %v", missingBinding, err)
			}
			gotBinding, err := ec.GetBinding(ctx, bindingID)
			if err != nil || gotBinding == nil || gotBinding.Name != "b1" {
				t.Fatalf("GetBinding: %+v %v", gotBinding, err)
			}
			if _, err := ec.ListBindings(ctx); err != nil {
				t.Fatalf("ListBindings: %v", err)
			}
			if err := ec.ApproveBinding(ctx, bindingID); err != nil {
				t.Fatalf("ApproveBinding from pending_approval = %v", err)
			}
			err = ec.ApproveBinding(ctx, bindingID)
			if !errors.Is(err, store.ErrBindingTransition) {
				t.Fatalf("ApproveBinding when armed = %v, want ErrBindingTransition", err)
			}
			err = ec.DeleteBinding(ctx, "rabsent00000000")
			if !errors.Is(err, store.ErrBindingNotFound) {
				t.Fatalf("DeleteBinding missing = %v, want ErrBindingNotFound", err)
			}

			// Capture + dispatch.
			captureID, err := ec.InsertCapture(ctx, store.CapturedEvent{Source: "sentry", Body: `{"id":1}`, Authenticated: true}, 0, 0)
			if err != nil || captureID == "" {
				t.Fatalf("InsertCapture: %v %v", captureID, err)
			}
			if _, err := ec.ListCaptures(ctx, 10); err != nil {
				t.Fatalf("ListCaptures: %v", err)
			}
			if _, err := ec.ArmedBindingsForSource(ctx, "sentry"); err != nil {
				t.Fatalf("ArmedBindingsForSource: %v", err)
			}
			bindingTask, err := c.EnqueueBindingTask(ctx, "acme", "widget", "t", "b", "implement", "", bindingID, 1)
			if err != nil || bindingTask == nil {
				t.Fatalf("EnqueueBindingTask: %+v %v", bindingTask, err)
			}
			if err := ec.RecordDispatch(ctx, bindingID, 1, captureID, bindingTask.ID); err != nil {
				t.Fatalf("RecordDispatch: %v", err)
			}
			err = ec.RecordDispatch(ctx, bindingID, 1, captureID, bindingTask.ID)
			if !errors.Is(err, store.ErrAlreadyDispatched) {
				t.Fatalf("RecordDispatch dup = %v, want ErrAlreadyDispatched", err)
			}
			if _, err := ec.ListUndispatchedCaptures(ctx, []string{"sentry"}, 10); err != nil {
				t.Fatalf("ListUndispatchedCaptures: %v", err)
			}

			// Playbook dispatch ledger: ErrAlreadyDispatched survives the hop
			// via errors.Is, and DeletePlaybookDispatches actually clears the
			// row (the same key records again after delete).
			if err := pc.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/7", "notify"); err != nil {
				t.Fatalf("RecordPlaybookDispatch: %v", err)
			}
			// Three of the four PRIMARY KEY components are carried through the hop
			// with a distinguishing value here: a client/server mapping that drops
			// or transposes event_id, playbook_version, or action_id collapses
			// these distinct rows onto the first tuple and fails one of the three
			// below.
			if err := pc.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/8", "notify"); err != nil {
				t.Fatalf("RecordPlaybookDispatch (distinct event_id) = %v, want success", err)
			}
			if err := pc.RecordPlaybookDispatch(ctx, "pb.yaml", "v2", "archie:acme/widget/7", "notify"); err != nil {
				t.Fatalf("RecordPlaybookDispatch (distinct playbook_version) = %v, want success", err)
			}
			if err := pc.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/7", "build"); err != nil {
				t.Fatalf("RecordPlaybookDispatch (distinct action_id) = %v, want success", err)
			}
			err = pc.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/7", "notify")
			if !errors.Is(err, store.ErrAlreadyDispatched) {
				t.Fatalf("RecordPlaybookDispatch dup = %v, want ErrAlreadyDispatched", err)
			}
			if err := pc.DeletePlaybookDispatches(ctx, "pb.yaml"); err != nil {
				t.Fatalf("DeletePlaybookDispatches: %v", err)
			}
			if err := pc.RecordPlaybookDispatch(ctx, "pb.yaml", "v1", "archie:acme/widget/7", "notify"); err != nil {
				t.Fatalf("RecordPlaybookDispatch after delete = %v, want success (delete must clear the row)", err)
			}
		})
	}
}

// TestConfigSnapshotContract drives the published configuration projection
// through both the local store and the gRPC client. The document is opaque to
// this hop, so the test's whole claim is that it arrives unchanged: the UI
// process renders exactly what the daemon published, and an absent snapshot
// reads as "not published yet" rather than an error.
func TestConfigSnapshotContract(t *testing.T) {
	for _, mode := range []string{"local", "grpc"} {
		t.Run(mode, func(t *testing.T) {
			local := store.OpenTest(t)
			var st contract = local
			if mode == "grpc" {
				st = remoteContract(t, local)
			}
			ctx := t.Context()

			if _, found, err := st.ConfigSnapshot(ctx); err != nil || found {
				t.Fatalf("unpublished snapshot = (found %v, %v), want (false, nil)", found, err)
			}

			published := store.ConfigSnapshot{
				Schema:      "webui.ConfigView/1",
				Document:    []byte(`{"identity":{"bot_user":"archie"},"providers":{"openai":{"api_key_env":"OPENAI_API_KEY","configured":true}}}`),
				PublishedAt: time.Date(2026, 9, 9, 12, 30, 0, 0, time.UTC),
			}
			if err := st.PutConfigSnapshot(ctx, published); err != nil {
				t.Fatalf("PutConfigSnapshot: %v", err)
			}

			got, found, err := st.ConfigSnapshot(ctx)
			if err != nil || !found {
				t.Fatalf("ConfigSnapshot = (found %v, %v), want the published snapshot", found, err)
			}
			if got.Schema != published.Schema {
				t.Errorf("schema = %q, want %q", got.Schema, published.Schema)
			}
			if string(got.Document) != string(published.Document) {
				t.Errorf("document = %s, want %s", got.Document, published.Document)
			}
			if !got.PublishedAt.Equal(published.PublishedAt) {
				t.Errorf("published at = %v, want %v", got.PublishedAt, published.PublishedAt)
			}
		})
	}
}
