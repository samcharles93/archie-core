package archied

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/secret"
)

// resourceWatchStub answers Query with one stored version per kind and Watch
// with the documents queued for the watched kind. It is the store end of the
// seam the runtime-resource watch meets a live update through, and it is the
// store: delivering a document stores it, so the layering the apply runs
// reads the update back from Query the way production does. A kind the map
// does not carry is answered codes.NotFound, the state a skipped seed leaves
// behind.
type resourceWatchStub struct {
	values map[string]any
	// version is the stored version until a watch delivers an update, which
	// is the version the store then answers Query with -- the apply the
	// delivery triggers layers and reports the version the store now holds.
	version int64
	// storedVersion, when present, is the version Query answers a kind with.
	storedVersion map[string]int64
	// watch queues, per kind, the documents a live write delivers after the
	// stored version, one per stream: the first open delivers the first
	// document, the second open the second, and so on.
	watch map[string][]string
	// opens counts how often each kind's stream was opened, so queued
	// documents are delivered in order across the reconnects keepWatch runs.
	opens map[string]int
}

func (s *resourceWatchStub) versionOf(kind string) int64 {
	if stored, ok := s.storedVersion[kind]; ok {
		return stored
	}
	return s.version
}

func (*resourceWatchStub) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	return &pb.CatalogResponse{}, nil
}

func (s *resourceWatchStub) Query(_ context.Context, request *pb.QueryRequest, _ ...grpc.CallOption) (*pb.QueryResponse, error) {
	value, ok := s.values[request.Kind]
	if !ok {
		return nil, status.Error(codes.NotFound, "resource not found")
	}
	encoded, err := json.Marshal(value)
	return &pb.QueryResponse{Resource: &pb.Resource{Kind: request.Kind, Version: s.versionOf(request.Kind), ValueJson: encoded}}, err
}

func (s *resourceWatchStub) Watch(_ context.Context, request *pb.WatchRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	// One queued document per stream: a stream ends after the document it
	// carries, and the next one arrives on the reconnect -- the shape a store
	// whose writes are spaced apart produces, and the shape that keeps the
	// apply each delivery triggers reading the document back from Query
	// before the store has moved on to the next one.
	open := s.opens[request.Kind]
	s.opens[request.Kind] = open + 1
	if open >= len(s.watch[request.Kind]) {
		return &watchStreamStub{}, nil
	}
	document := s.watch[request.Kind][open]
	version := s.version + int64(open) + 1
	if version <= request.AfterVersion {
		return &watchStreamStub{}, nil
	}
	// The stub is the store: a document is stored when the stream delivers
	// it, so the apply that delivery triggers layers the document back from
	// Query.
	response := &pb.WatchResponse{Resource: &pb.Resource{
		Kind: request.Kind, Version: version, ValueJson: []byte(document),
	}}
	return &watchStreamStub{responses: []*pb.WatchResponse{response}, onDeliver: func() {
		s.values[request.Kind] = json.RawMessage(document)
		s.storedVersion[request.Kind] = version
	}}, nil
}

func (*resourceWatchStub) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
}

func (*resourceWatchStub) Audit(context.Context, *pb.AuditRequest, ...grpc.CallOption) (*pb.AuditResponse, error) {
	return &pb.AuditResponse{}, nil
}

func (*resourceWatchStub) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("unexpected Command")
}

// newResourceWatchBoot builds a boot whose apply status lands in a recorder,
// with the chat model runtime a provider or role update must rebuild.
func newResourceWatchBoot(t *testing.T, stub *resourceWatchStub) (*boot, *applyStatusRecorder) {
	t.Helper()
	b, recorder := newLiveApplyBoot(t, fileConfig())
	b.controlPlane = controlplane.NewRPCClient(stub)
	b.secrets = secret.NewRegistry()
	b.chatModels = newChatModelManager(fileConfig().Models)
	b.setLLM(agentexec.NewRuntime(executionProviders(fileConfig())))
	return b, recorder
}

// schedulingDocument renders a scheduling-policy document the layering reads
// back, so the applied and refused updates in these tests cannot disagree on
// what "the stored scheduling policy" is.
func schedulingDocument(interval string) string {
	return `{"poll_interval": "` + interval + `", "max_retries": 7, "dispatch": {"trigger": "assignee"}}`
}

// TestRuntimeResourceWatchReLayersTheStoredChangeAndRepublishes is the live
// apply for the four kinds the daemon watches (archie-core-zfb0.1): a new
// stored version of scheduling-policy re-runs the layering a reload runs and
// republishes through config.Holder, so the change takes effect without a
// restart, the apply status records the version that was applied, and every
// other database-owned setting the layering read is still in the running
// config.
func TestRuntimeResourceWatchReLayersTheStoredChangeAndRepublishes(t *testing.T) {
	values := databaseOwnedResources()
	stub := &resourceWatchStub{values: values, version: 3, storedVersion: map[string]int64{}, opens: map[string]int{}, watch: map[string][]string{
		controlplane.SchedulingPolicyKind: {schedulingDocument("5m")},
	}}
	b, status := newResourceWatchBoot(t, stub)
	if err := b.loadRuntimeConfig(t.Context()); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := b.startRuntimeResourceWatches(ctx, b.runtimeVersions); err != nil {
		t.Fatalf("startRuntimeResourceWatches: %v", err)
	}
	// One record for boot's layering, one for the watched update.
	records := status.awaitCount(t, controlplane.SchedulingPolicyKind, 2)

	cfg := b.cfgHolder.Get()
	if got := time.Duration(cfg.PollInterval); got != 5*time.Minute {
		t.Errorf("running PollInterval = %s, want the watched update (5m)", got)
	}
	if got := cfg.Repos[0].Name; got != "from-database" {
		t.Errorf("running Repos[0] = %q, want the other kinds' last values to survive the re-layering", got)
	}
	last := records[len(records)-1]
	if last.AppliedVersion != stub.version+1 {
		t.Errorf("apply status version = %d, want the delivered version %d", last.AppliedVersion, stub.version+1)
	}
	if last.Error != "" {
		t.Errorf("apply status error = %q, want none for an applied update", last.Error)
	}
}

// TestRuntimeResourceWatchKeepsLastKnownGoodWhenTheUpdateIsRefused: an update
// the layered document cannot run is refused before it is published, the
// running config keeps the last version that was applied, and the refusal is
// reported through apply status against the version still live.
func TestRuntimeResourceWatchKeepsLastKnownGoodWhenTheUpdateIsRefused(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 3, storedVersion: map[string]int64{}, opens: map[string]int{}, watch: map[string][]string{
		controlplane.SchedulingPolicyKind: {schedulingDocument("5m"), schedulingDocument("0s")},
	}}
	b, status := newResourceWatchBoot(t, stub)
	if err := b.loadRuntimeConfig(t.Context()); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := b.startRuntimeResourceWatches(ctx, b.runtimeVersions); err != nil {
		t.Fatalf("startRuntimeResourceWatches: %v", err)
	}
	// One record for boot's layering, one for the applied update, one for the
	// refusal. The recorder keeps the version that is still live on a refusal,
	// so the third record carries the second update's version.
	records := status.awaitCount(t, controlplane.SchedulingPolicyKind, 3)

	if got := time.Duration(b.cfgHolder.Get().PollInterval); got != 5*time.Minute {
		t.Errorf("running PollInterval = %s, want the last update that validated (5m)", got)
	}
	last := records[len(records)-1]
	if last.AppliedVersion != stub.version+1 {
		t.Errorf("apply status version = %d, want the version still live (%d)", last.AppliedVersion, stub.version+1)
	}
	if !strings.Contains(last.Error, "poll_interval must be positive") {
		t.Errorf("apply status error = %q, want it to carry the refusal", last.Error)
	}
}

// TestLiveModelRoleUpdateRebuildsTheChatModelRuntime: providers and model
// role assignments also feed the gateway chat runtime, so an update to either
// kind swaps the runtime (ai-sdk's Runtime caches the provider instances it
// built) and re-derives the model list the chat surfaces offer.
func TestLiveModelRoleUpdateRebuildsTheChatModelRuntime(t *testing.T) {
	tests := []struct {
		name string
		kind string
	}{
		{"a provider settings update", controlplane.ProviderSettingsKind},
		{"a model role assignments update", controlplane.ModelRoleAssignmentsKind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := databaseOwnedResources()
			values[controlplane.ModelRoleAssignmentsKind] = map[string]string{
				"builder": "main/db-model", "chat": "main/db-model",
			}
			stub := &resourceWatchStub{values: values, version: 3, storedVersion: map[string]int64{}, opens: map[string]int{}}
			b, _ := newResourceWatchBoot(t, stub)
			if err := b.loadRuntimeConfig(t.Context()); err != nil {
				t.Fatalf("loadRuntimeConfig: %v", err)
			}
			before := b.chatLLM()

			b.applyRuntimeResourceUpdate(t.Context(), tt.kind, controlplane.AppliedResource{Version: 4})

			if after := b.chatLLM(); after == before {
				t.Error("the chat runtime was not rebuilt; the next turn keeps the previous provider set")
			}
			if !slices.Contains(b.chatModels.Models(), "main/db-model") {
				t.Errorf("chat models = %v, want the new role assignment's reference", b.chatModels.Models())
			}
			if got := b.chatModels.ActiveModel(); got != "main/db-model" {
				t.Errorf("chat active model = %q, want the chat role's model after the update", got)
			}
			// The running config carries the update, not just the chat runtime.
			if got := b.cfgHolder.Get().Models["chat"]; got != "main/db-model" {
				t.Errorf("running chat role = %q, want the applied assignment", got)
			}
		})
	}
}
