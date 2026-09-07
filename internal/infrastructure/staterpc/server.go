// Package staterpc adapts the State Store contract (internal/store's
// producer-owned surfaces plus the consumer-owned workflow.Store) to gRPC.
// See docs/prds/state-store-contract.md for the ratified contract this
// package implements against.
package staterpc

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/store"
)

// Unavailable-capability errors, mirroring gatewayrpc's missing-session-store
// pattern (docs/prds/state-store-contract.md §7's "capability absent ->
// Unavailable" row): a nil optional Deps field maps to codes.Unavailable
// rather than a nil-pointer panic.
var (
	errCaptureUnavailable            = status.Error(codes.Unavailable, "capture store unavailable")
	errMappingUnavailable            = status.Error(codes.Unavailable, "mapping store unavailable")
	errBindingUnavailable            = status.Error(codes.Unavailable, "binding store unavailable")
	errBindingDispatchUnavailable    = status.Error(codes.Unavailable, "binding dispatcher unavailable")
	errBindingTaskCreatorUnavailable = status.Error(codes.Unavailable, "binding task creator unavailable")
)

func timeSeconds(s int64) time.Duration { return time.Duration(s) * time.Second }

// Deps groups the store surfaces the StateStore service fronts. TaskStore is
// required; the rest are optional (nil disables that group's RPCs, returning
// codes.Unavailable) exactly as their daemon-side consumers already treat a
// nil Mappings/Bindings/BindingDispatcher/BindingTaskCreator as "disabled".
type Deps struct {
	Grants             *TaskGrants
	Tasks              store.TaskStore
	Captures           store.CaptureStore
	Mappings           store.MappingStore
	Bindings           store.BindingStore
	BindingDispatcher  store.BindingDispatcher
	BindingTaskCreator store.BindingTaskCreator
	Log                *slog.Logger
}

type server struct {
	pb.UnimplementedStateStoreServiceServer
	deps Deps
}

// RegisterServer registers the StateStore gRPC service against registrar,
// backed by deps. Optionally wrap registrar's interceptor chain with
// TokenInterceptor for the bridge-address (agent-consumed) topology; the
// caller decides, per docs/prds/state-store-contract.md §9's listener rule.
func RegisterServer(registrar grpc.ServiceRegistrar, deps Deps) {
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	pb.RegisterStateStoreServiceServer(registrar, &server{deps: deps})
}

func (s *server) logErr(rpc string, err error) error {
	if err != nil {
		s.deps.Log.Error("staterpc call failed", "rpc", rpc, "err", err)
	}
	return mapError(err)
}

// Lifecycle

func (s *server) EnqueueIssue(ctx context.Context, r *pb.EnqueueIssueRequest) (*pb.EnqueueIssueResponse, error) {
	inserted, err := s.deps.Tasks.EnqueueIssue(ctx, r.Owner, r.Repo, int(r.Number), r.Title, r.Body, r.Labels, r.Identity)
	if err != nil {
		return nil, s.logErr("EnqueueIssue", err)
	}
	return &pb.EnqueueIssueResponse{Inserted: inserted}, nil
}

func (s *server) EnqueueChatTask(ctx context.Context, r *pb.EnqueueChatTaskRequest) (*pb.EnqueueChatTaskResponse, error) {
	t, err := s.deps.Tasks.EnqueueChatTask(ctx, r.Owner, r.Repo, r.Title, r.Body, r.Workflow, r.Identity)
	if err != nil {
		return nil, s.logErr("EnqueueChatTask", err)
	}
	return &pb.EnqueueChatTaskResponse{Task: taskProto(t)}, nil
}

func (s *server) ClaimNext(ctx context.Context, _ *pb.ClaimNextRequest) (*pb.ClaimNextResponse, error) {
	t, err := s.deps.Tasks.ClaimNext(ctx)
	if err != nil {
		return nil, s.logErr("ClaimNext", err)
	}
	return &pb.ClaimNextResponse{Task: taskProto(t), Found: t != nil}, nil
}

func (s *server) ClaimByIssue(ctx context.Context, r *pb.ClaimByIssueRequest) (*pb.ClaimByIssueResponse, error) {
	t, err := s.deps.Tasks.ClaimByIssue(ctx, r.Owner, r.Repo, int(r.Number))
	if err != nil {
		return nil, s.logErr("ClaimByIssue", err)
	}
	return &pb.ClaimByIssueResponse{Task: taskProto(t), Found: t != nil}, nil
}

func (s *server) Transition(ctx context.Context, r *pb.TransitionRequest) (*pb.TransitionResponse, error) {
	if err := s.deps.Tasks.Transition(ctx, r.TaskId, r.From, r.To, r.Detail); err != nil {
		return nil, s.logErr("Transition", err)
	}
	return &pb.TransitionResponse{}, nil
}

func (s *server) Update(ctx context.Context, r *pb.UpdateRequest) (*pb.UpdateResponse, error) {
	if r.Task == nil {
		return nil, status.Error(codes.InvalidArgument, "task is required")
	}
	if err := s.deps.Tasks.Update(ctx, taskValue(r.Task)); err != nil {
		return nil, s.logErr("Update", err)
	}
	return &pb.UpdateResponse{}, nil
}

func (s *server) Requeue(ctx context.Context, r *pb.RequeueRequest) (*pb.RequeueResponse, error) {
	if err := s.deps.Tasks.Requeue(ctx, r.TaskId, r.FromStatus, r.Workflow); err != nil {
		return nil, s.logErr("Requeue", err)
	}
	return &pb.RequeueResponse{}, nil
}

func (s *server) RecoverStale(ctx context.Context, _ *pb.RecoverStaleRequest) (*pb.RecoverStaleResponse, error) {
	n, err := s.deps.Tasks.RecoverStale(ctx)
	if err != nil {
		return nil, s.logErr("RecoverStale", err)
	}
	return &pb.RecoverStaleResponse{Count: n}, nil
}

// Archive / Retry

func (s *server) ArchiveTask(ctx context.Context, r *pb.ArchiveTaskRequest) (*pb.ArchiveTaskResponse, error) {
	eventID, err := s.deps.Tasks.ArchiveTask(ctx, r.TaskId, r.FromStatus, eventValue(r.Audit))
	if err != nil {
		return nil, s.logErr("ArchiveTask", err)
	}
	return &pb.ArchiveTaskResponse{EventId: eventID}, nil
}

func (s *server) RetryTask(ctx context.Context, r *pb.RetryTaskRequest) (*pb.RetryTaskResponse, error) {
	if err := s.deps.Tasks.RetryTask(ctx, r.TaskId, r.FromStatus, r.Workflow); err != nil {
		return nil, s.logErr("RetryTask", err)
	}
	return &pb.RetryTaskResponse{}, nil
}

// Queries

func (s *server) TaskByIssue(ctx context.Context, r *pb.TaskByIssueRequest) (*pb.TaskByIssueResponse, error) {
	t, err := s.deps.Tasks.TaskByIssue(ctx, r.Owner, r.Repo, int(r.Number))
	if err != nil {
		return nil, s.logErr("TaskByIssue", err)
	}
	return &pb.TaskByIssueResponse{Task: taskProto(t), Found: t != nil}, nil
}

func (s *server) TaskByID(ctx context.Context, r *pb.TaskByIDRequest) (*pb.TaskByIDResponse, error) {
	t, err := s.deps.Tasks.TaskByID(ctx, r.TaskId)
	if err != nil {
		return nil, s.logErr("TaskByID", err)
	}
	return &pb.TaskByIDResponse{Task: taskProto(t), Found: t != nil}, nil
}

func (s *server) OpenPRs(ctx context.Context, _ *pb.OpenPRsRequest) (*pb.OpenPRsResponse, error) {
	tasks, err := s.deps.Tasks.OpenPRs(ctx)
	if err != nil {
		return nil, s.logErr("OpenPRs", err)
	}
	out := make([]*pb.Task, len(tasks))
	for i := range tasks {
		out[i] = taskProto(&tasks[i])
	}
	return &pb.OpenPRsResponse{Tasks: out}, nil
}

func (s *server) ClearTerminalTasks(ctx context.Context, _ *pb.ClearTerminalTasksRequest) (*pb.ClearTerminalTasksResponse, error) {
	n, err := s.deps.Tasks.ClearTerminalTasks(ctx)
	if err != nil {
		return nil, s.logErr("ClearTerminalTasks", err)
	}
	return &pb.ClearTerminalTasksResponse{Count: n}, nil
}

func (s *server) Tasks(ctx context.Context, r *pb.TasksRequest) (*pb.TasksResponse, error) {
	tasks, err := s.deps.Tasks.Tasks(ctx, int(r.Limit))
	if err != nil {
		return nil, s.logErr("Tasks", err)
	}
	out := make([]*pb.Task, len(tasks))
	for i := range tasks {
		out[i] = taskProto(&tasks[i])
	}
	return &pb.TasksResponse{Tasks: out}, nil
}

func (s *server) StatusCounts(ctx context.Context, _ *pb.StatusCountsRequest) (*pb.StatusCountsResponse, error) {
	counts, err := s.deps.Tasks.StatusCounts(ctx)
	if err != nil {
		return nil, s.logErr("StatusCounts", err)
	}
	out := make(map[string]int64, len(counts))
	for k, v := range counts {
		out[k] = int64(v)
	}
	return &pb.StatusCountsResponse{Counts: out}, nil
}

func (s *server) IncrementRetryCount(ctx context.Context, r *pb.IncrementRetryCountRequest) (*pb.IncrementRetryCountResponse, error) {
	if err := s.deps.Tasks.IncrementRetryCount(ctx, r.TaskId); err != nil {
		return nil, s.logErr("IncrementRetryCount", err)
	}
	return &pb.IncrementRetryCountResponse{}, nil
}

// Events

func (s *server) InsertEvent(ctx context.Context, r *pb.InsertEventRequest) (*pb.InsertEventResponse, error) {
	id, err := s.deps.Tasks.InsertEvent(ctx, eventValue(r.Event))
	if err != nil {
		return nil, s.logErr("InsertEvent", err)
	}
	return &pb.InsertEventResponse{Id: id}, nil
}

func (s *server) EventsSince(ctx context.Context, r *pb.EventsSinceRequest) (*pb.EventsSinceResponse, error) {
	evs, err := s.deps.Tasks.EventsSince(ctx, r.SinceId, int(r.Limit))
	if err != nil {
		return nil, s.logErr("EventsSince", err)
	}
	return &pb.EventsSinceResponse{Events: mapValues(evs, eventProto)}, nil
}

func (s *server) TaskEvents(ctx context.Context, r *pb.TaskEventsRequest) (*pb.TaskEventsResponse, error) {
	evs, err := s.deps.Tasks.TaskEvents(ctx, r.TaskId)
	if err != nil {
		return nil, s.logErr("TaskEvents", err)
	}
	return &pb.TaskEventsResponse{Events: mapValues(evs, eventProto)}, nil
}

func (s *server) WorkflowStats(ctx context.Context, _ *pb.WorkflowStatsRequest) (*pb.WorkflowStatsResponse, error) {
	stats, err := s.deps.Tasks.WorkflowStats(ctx)
	if err != nil {
		return nil, s.logErr("WorkflowStats", err)
	}
	return &pb.WorkflowStatsResponse{Stats: mapValues(stats, workflowStatProto)}, nil
}

func (s *server) StageStats(ctx context.Context, _ *pb.StageStatsRequest) (*pb.StageStatsResponse, error) {
	stats, err := s.deps.Tasks.StageStats(ctx)
	if err != nil {
		return nil, s.logErr("StageStats", err)
	}
	return &pb.StageStatsResponse{Stats: mapValues(stats, stageStatProto)}, nil
}

func (s *server) TokensByDay(ctx context.Context, r *pb.TokensByDayRequest) (*pb.TokensByDayResponse, error) {
	tokens, err := s.deps.Tasks.TokensByDay(ctx, int(r.Days))
	if err != nil {
		return nil, s.logErr("TokensByDay", err)
	}
	return &pb.TokensByDayResponse{Tokens: mapValues(tokens, dayTokensProto)}, nil
}

// Capture

func (s *server) capture() (store.CaptureStore, error) {
	if s.deps.Captures == nil {
		return nil, errCaptureUnavailable
	}
	return s.deps.Captures, nil
}

func (s *server) InsertCapture(ctx context.Context, r *pb.InsertCaptureRequest) (*pb.InsertCaptureResponse, error) {
	cs, err := s.capture()
	if err != nil {
		return nil, err
	}
	id, err := cs.InsertCapture(ctx, capturedEventValue(r.Capture), timeSeconds(r.RetentionSeconds), int(r.MaxEvents))
	if err != nil {
		return nil, s.logErr("InsertCapture", err)
	}
	return &pb.InsertCaptureResponse{Id: id}, nil
}

func (s *server) ListCaptures(ctx context.Context, r *pb.ListCapturesRequest) (*pb.ListCapturesResponse, error) {
	cs, err := s.capture()
	if err != nil {
		return nil, err
	}
	captures, err := cs.ListCaptures(ctx, int(r.Limit))
	if err != nil {
		return nil, s.logErr("ListCaptures", err)
	}
	return &pb.ListCapturesResponse{Captures: mapValues(captures, capturedEventProto)}, nil
}

// StreamCaptures supersedes ListCaptures: a batch of large capture bodies in
// one unary response can exceed gRPC's 4MiB message cap
// (docs/prds/state-store-contract.md). ListCaptures stays implemented,
// unmodified, so an old client/server pairing mid-rollout keeps working.
func (s *server) StreamCaptures(r *pb.StreamCapturesRequest, stream pb.StateStoreService_StreamCapturesServer) error {
	cs, err := s.capture()
	if err != nil {
		return err
	}
	captures, err := cs.ListCaptures(stream.Context(), int(r.Limit))
	if err != nil {
		return s.logErr("StreamCaptures", err)
	}
	for _, c := range captures {
		if err := stream.Send(&pb.StreamCapturesResponse{Capture: capturedEventProto(c)}); err != nil {
			return err
		}
	}
	return nil
}

// Mapping

func (s *server) mapping() (store.MappingStore, error) {
	if s.deps.Mappings == nil {
		return nil, errMappingUnavailable
	}
	return s.deps.Mappings, nil
}

func (s *server) InsertMapping(ctx context.Context, r *pb.InsertMappingRequest) (*pb.InsertMappingResponse, error) {
	ms, err := s.mapping()
	if err != nil {
		return nil, err
	}
	id, err := ms.InsertMapping(ctx, mappingValue(r.Mapping))
	if err != nil {
		return nil, s.logErr("InsertMapping", err)
	}
	return &pb.InsertMappingResponse{Id: id}, nil
}

func (s *server) GetMapping(ctx context.Context, r *pb.GetMappingRequest) (*pb.GetMappingResponse, error) {
	ms, err := s.mapping()
	if err != nil {
		return nil, err
	}
	m, err := ms.GetMapping(ctx, r.Id)
	if err != nil {
		return nil, s.logErr("GetMapping", err)
	}
	return &pb.GetMappingResponse{Mapping: mappingProtoPtr(m), Found: m != nil}, nil
}

func (s *server) ListMappings(ctx context.Context, _ *pb.ListMappingsRequest) (*pb.ListMappingsResponse, error) {
	ms, err := s.mapping()
	if err != nil {
		return nil, err
	}
	mappings, err := ms.ListMappings(ctx)
	if err != nil {
		return nil, s.logErr("ListMappings", err)
	}
	return &pb.ListMappingsResponse{Mappings: mapValues(mappings, mappingProto)}, nil
}

func (s *server) UpdateMapping(ctx context.Context, r *pb.UpdateMappingRequest) (*pb.UpdateMappingResponse, error) {
	ms, err := s.mapping()
	if err != nil {
		return nil, err
	}
	if err := ms.UpdateMapping(ctx, mappingValue(r.Mapping)); err != nil {
		return nil, s.logErr("UpdateMapping", err)
	}
	return &pb.UpdateMappingResponse{}, nil
}

func (s *server) DeleteMapping(ctx context.Context, r *pb.DeleteMappingRequest) (*pb.DeleteMappingResponse, error) {
	ms, err := s.mapping()
	if err != nil {
		return nil, err
	}
	if err := ms.DeleteMapping(ctx, r.Id); err != nil {
		return nil, s.logErr("DeleteMapping", err)
	}
	return &pb.DeleteMappingResponse{}, nil
}

// Binding

func (s *server) binding() (store.BindingStore, error) {
	if s.deps.Bindings == nil {
		return nil, errBindingUnavailable
	}
	return s.deps.Bindings, nil
}

func (s *server) InsertBinding(ctx context.Context, r *pb.InsertBindingRequest) (*pb.InsertBindingResponse, error) {
	bs, err := s.binding()
	if err != nil {
		return nil, err
	}
	id, err := bs.InsertBinding(ctx, bindingValue(r.Binding))
	if err != nil {
		return nil, s.logErr("InsertBinding", err)
	}
	return &pb.InsertBindingResponse{Id: id}, nil
}

func (s *server) GetBinding(ctx context.Context, r *pb.GetBindingRequest) (*pb.GetBindingResponse, error) {
	bs, err := s.binding()
	if err != nil {
		return nil, err
	}
	b, err := bs.GetBinding(ctx, r.Id)
	if err != nil {
		return nil, s.logErr("GetBinding", err)
	}
	return &pb.GetBindingResponse{Binding: bindingProtoPtr(b), Found: b != nil}, nil
}

func (s *server) ListBindings(ctx context.Context, _ *pb.ListBindingsRequest) (*pb.ListBindingsResponse, error) {
	bs, err := s.binding()
	if err != nil {
		return nil, err
	}
	bindings, err := bs.ListBindings(ctx)
	if err != nil {
		return nil, s.logErr("ListBindings", err)
	}
	return &pb.ListBindingsResponse{Bindings: mapValues(bindings, bindingProto)}, nil
}

func (s *server) UpdateBinding(ctx context.Context, r *pb.UpdateBindingRequest) (*pb.UpdateBindingResponse, error) {
	bs, err := s.binding()
	if err != nil {
		return nil, err
	}
	if err := bs.UpdateBinding(ctx, bindingValue(r.Binding)); err != nil {
		return nil, s.logErr("UpdateBinding", err)
	}
	return &pb.UpdateBindingResponse{}, nil
}

func (s *server) DeleteBinding(ctx context.Context, r *pb.DeleteBindingRequest) (*pb.DeleteBindingResponse, error) {
	bs, err := s.binding()
	if err != nil {
		return nil, err
	}
	if err := bs.DeleteBinding(ctx, r.Id); err != nil {
		return nil, s.logErr("DeleteBinding", err)
	}
	return &pb.DeleteBindingResponse{}, nil
}

func (s *server) ApproveBinding(ctx context.Context, r *pb.ApproveBindingRequest) (*pb.ApproveBindingResponse, error) {
	bs, err := s.binding()
	if err != nil {
		return nil, err
	}
	if err := bs.ApproveBinding(ctx, r.Id); err != nil {
		return nil, s.logErr("ApproveBinding", err)
	}
	return &pb.ApproveBindingResponse{}, nil
}

// Dispatch

func (s *server) ArmedBindingsForSource(ctx context.Context, r *pb.ArmedBindingsForSourceRequest) (*pb.ArmedBindingsForSourceResponse, error) {
	if s.deps.BindingDispatcher == nil {
		return nil, errBindingDispatchUnavailable
	}
	bindings, err := s.deps.BindingDispatcher.ArmedBindingsForSource(ctx, r.Source)
	if err != nil {
		return nil, s.logErr("ArmedBindingsForSource", err)
	}
	return &pb.ArmedBindingsForSourceResponse{Bindings: mapValues(bindings, bindingProto)}, nil
}

func (s *server) RecordDispatch(ctx context.Context, r *pb.RecordDispatchRequest) (*pb.RecordDispatchResponse, error) {
	if s.deps.BindingDispatcher == nil {
		return nil, errBindingDispatchUnavailable
	}
	if err := s.deps.BindingDispatcher.RecordDispatch(ctx, r.BindingId, r.BindingVersion, r.CaptureId, r.TaskId); err != nil {
		return nil, s.logErr("RecordDispatch", err)
	}
	return &pb.RecordDispatchResponse{}, nil
}

func (s *server) ListUndispatchedCaptures(ctx context.Context, r *pb.ListUndispatchedCapturesRequest) (*pb.ListUndispatchedCapturesResponse, error) {
	if s.deps.BindingDispatcher == nil {
		return nil, errBindingDispatchUnavailable
	}
	captures, err := s.deps.BindingDispatcher.ListUndispatchedCaptures(ctx, r.Sources, int(r.Limit))
	if err != nil {
		return nil, s.logErr("ListUndispatchedCaptures", err)
	}
	return &pb.ListUndispatchedCapturesResponse{Captures: mapValues(captures, capturedEventProto)}, nil
}

// StreamUndispatchedCaptures supersedes ListUndispatchedCaptures; see
// StreamCaptures above.
func (s *server) StreamUndispatchedCaptures(r *pb.StreamUndispatchedCapturesRequest, stream pb.StateStoreService_StreamUndispatchedCapturesServer) error {
	if s.deps.BindingDispatcher == nil {
		return errBindingDispatchUnavailable
	}
	captures, err := s.deps.BindingDispatcher.ListUndispatchedCaptures(stream.Context(), r.Sources, int(r.Limit))
	if err != nil {
		return s.logErr("StreamUndispatchedCaptures", err)
	}
	for _, c := range captures {
		if err := stream.Send(&pb.StreamUndispatchedCapturesResponse{Capture: capturedEventProto(c)}); err != nil {
			return err
		}
	}
	return nil
}

// BindingTaskCreator

func (s *server) EnqueueBindingTask(ctx context.Context, r *pb.EnqueueBindingTaskRequest) (*pb.EnqueueBindingTaskResponse, error) {
	if s.deps.BindingTaskCreator == nil {
		return nil, errBindingTaskCreatorUnavailable
	}
	t, err := s.deps.BindingTaskCreator.EnqueueBindingTask(ctx, r.Owner, r.Repo, r.Title, r.Body, r.Workflow, r.Identity, r.BindingId, int(r.BindingVersion))
	if err != nil {
		return nil, s.logErr("EnqueueBindingTask", err)
	}
	return &pb.EnqueueBindingTaskResponse{Task: taskProto(t)}, nil
}

func mappingProtoPtr(m *mapping.Mapping) *pb.Mapping {
	if m == nil {
		return nil
	}
	return mappingProto(*m)
}

func bindingProtoPtr(b *binding.Binding) *pb.Binding {
	if b == nil {
		return nil
	}
	return bindingProto(*b)
}
