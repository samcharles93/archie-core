// Package staterpc adapts the State Store contract (internal/domain/storecontract's
// producer-owned surfaces plus the consumer-owned workflow.Store) to gRPC.
// See docs/prds/state-store-contract.md for the ratified contract this
// package implements against.
package staterpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/logging"
)

// Unavailable-capability errors, mirroring gatewayrpc's missing-session-store
// pattern (docs/prds/state-store-contract.md §7's "capability absent ->
// Unavailable" row): a nil optional Deps field maps to codes.Unavailable
// rather than a nil-pointer panic.
var (
	errCaptureUnavailable            = status.Error(codes.Unavailable, "capture store unavailable")
	errConfigSnapshotsUnavailable    = status.Error(codes.Unavailable, "config snapshot store unavailable")
	errChannelStatusUnavailable      = status.Error(codes.Unavailable, "channel status store unavailable")
	errApplyStatusUnavailable        = status.Error(codes.Unavailable, "apply status store unavailable")
	errMappingUnavailable            = status.Error(codes.Unavailable, "mapping store unavailable")
	errBindingUnavailable            = status.Error(codes.Unavailable, "binding store unavailable")
	errSourceUnavailable             = status.Error(codes.Unavailable, "source store unavailable")
	errBindingDispatchUnavailable    = status.Error(codes.Unavailable, "binding dispatcher unavailable")
	errBindingTaskCreatorUnavailable = status.Error(codes.Unavailable, "binding task creator unavailable")
	errPlaybookDispatcherUnavailable = status.Error(codes.Unavailable, "playbook dispatcher unavailable")
	// errTaskLogsUnavailable is the "this process cannot read task logs at
	// all" answer, and it is deliberately distinct from a found=false read
	// result: only the first is a deployment matter. A dashboard that receives
	// this reports that the log cannot be read here; it must never turn it
	// into "task logging was not enabled for this run", which is what the
	// absent-handle path used to do.
	errTaskLogsUnavailable = status.Error(codes.Unavailable, msgTaskLogsUnavailable)
)

func timeSeconds(s int64) time.Duration { return time.Duration(s) * time.Second }

// taskLogChunkBytes bounds one streamed message. A log file rotates at
// logging.DefaultMaxSizeMB, well past gRPC's 4MiB unary cap, so the download
// is served in pieces (the same reason StreamCaptures superseded ListCaptures)
// and the pieces must be small enough to carry.
const taskLogChunkBytes = 256 << 10

// Deps groups the store surfaces the StateStore service fronts. TaskStore is
// required; the rest are optional (nil disables that group's RPCs, returning
// codes.Unavailable) exactly as their daemon-side consumers already treat a
// nil Mappings/Bindings/BindingDispatcher/BindingTaskCreator as "disabled".
type Deps struct {
	ControlPlane controlpb.ControlPlaneServiceServer
	Identities   identity.Repository
	// SubjectBindings fronts the subject-binding surface, which only the
	// authenticating path needs. Unset means this process cannot resolve a
	// verified subject, and the RPCs answer Unavailable rather than pretending.
	SubjectBindings identity.SubjectBinding
	Grants          *TaskGrants
	Tasks           storecontract.TaskStore
	Captures        storecontract.CaptureStore
	ConfigSnapshots storecontract.ConfigSnapshotStore
	// ChannelStatus is the channel runtime state the process hosting the channels
	// reports. Optional: nil disables the pair with codes.Unavailable, which is
	// the honest answer for a store service no messaging process is writing to.
	ChannelStatus storecontract.ChannelStatusStore
	ApplyStatus   storecontract.ApplyStatusStore
	Mappings      storecontract.MappingStore
	EventTypes    storecontract.EventTypeStore
	// MappingMatches counts the events each mapping resolved. Optional: nil
	// answers RecordMappingMatch with codes.Unavailable.
	MappingMatches     storecontract.MappingMatchRecorder
	Bindings           storecontract.BindingStore
	Sources            storecontract.SourceStore
	BindingDispatcher  storecontract.BindingDispatcher
	BindingTaskCreator storecontract.BindingTaskCreator
	// PlaybookDispatcher is the side-effecting-action idempotency ledger
	// (docs/prds/eda-playbook-engine.md gap 2). Optional: nil disables the
	// two playbook dispatch RPCs with codes.Unavailable.
	PlaybookDispatcher storecontract.PlaybookDispatcher
	// TaskLogs reads one task attempt's persisted log out of the state
	// directory this process owns. Optional, and nil is the honest default for
	// a store service that shares no state directory with the daemon:
	// ReadTaskLog then answers codes.Unavailable, which the dashboard reports
	// as "this process cannot read logs" rather than as "this attempt has no
	// log". Conflating those two is the bug this contract exists to fix.
	TaskLogs storecontract.TaskLogStore
	Log      *slog.Logger
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
	if deps.ControlPlane != nil {
		controlpb.RegisterControlPlaneServiceServer(registrar, deps.ControlPlane)
	}
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

func (s *server) ParkTask(ctx context.Context, r *pb.ParkTaskRequest) (*pb.ParkTaskResponse, error) {
	if err := s.deps.Tasks.ParkTask(ctx, r.TaskId, r.From, r.Detail, r.Class); err != nil {
		return nil, s.logErr("ParkTask", err)
	}
	return &pb.ParkTaskResponse{}, nil
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

func (s *server) BeginRemediation(ctx context.Context, r *pb.BeginRemediationRequest) (*pb.BeginRemediationResponse, error) {
	if err := s.deps.Tasks.BeginRemediation(ctx, r.TaskId, r.Payload); err != nil {
		return nil, s.logErr("BeginRemediation", err)
	}
	return &pb.BeginRemediationResponse{}, nil
}

func (s *server) UpdateReviewPayload(ctx context.Context, r *pb.UpdateReviewPayloadRequest) (*pb.UpdateReviewPayloadResponse, error) {
	if err := s.deps.Tasks.UpdateReviewPayload(ctx, r.TaskId, r.Payload); err != nil {
		return nil, s.logErr("UpdateReviewPayload", err)
	}
	return &pb.UpdateReviewPayloadResponse{}, nil
}

func (s *server) SetReviewCursors(ctx context.Context, r *pb.SetReviewCursorsRequest) (*pb.SetReviewCursorsResponse, error) {
	if err := s.deps.Tasks.SetReviewCursors(ctx, r.TaskId, r.ReviewCursor, r.CommentCursor); err != nil {
		return nil, s.logErr("SetReviewCursors", err)
	}
	return &pb.SetReviewCursorsResponse{}, nil
}

// Queries

func (s *server) TaskByIssue(ctx context.Context, r *pb.TaskByIssueRequest) (*pb.TaskByIssueResponse, error) {
	t, err := s.deps.Tasks.TaskByIssue(ctx, r.Owner, r.Repo, int(r.Number))
	if err != nil {
		return nil, s.logErr("TaskByIssue", err)
	}
	return &pb.TaskByIssueResponse{Task: taskProto(t), Found: t != nil}, nil
}

func (s *server) OpenTaskByPR(ctx context.Context, r *pb.OpenTaskByPRRequest) (*pb.OpenTaskByPRResponse, error) {
	t, err := s.deps.Tasks.OpenTaskByPR(ctx, r.Owner, r.Repo, int(r.Number))
	if err != nil {
		return nil, s.logErr("OpenTaskByPR", err)
	}
	return &pb.OpenTaskByPRResponse{Task: taskProto(t), Found: t != nil}, nil
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

func (s *server) capture() (storecontract.CaptureStore, error) {
	if s.deps.Captures == nil {
		return nil, errCaptureUnavailable
	}
	return s.deps.Captures, nil
}

func (s *server) applyStatus() (storecontract.ApplyStatusStore, error) {
	if s.deps.ApplyStatus == nil {
		return nil, errApplyStatusUnavailable
	}
	return s.deps.ApplyStatus, nil
}

func (s *server) PutApplyStatus(ctx context.Context, r *pb.PutApplyStatusRequest) (*pb.PutApplyStatusResponse, error) {
	as, err := s.applyStatus()
	if err != nil {
		return nil, err
	}
	if err := as.PutApplyStatus(ctx, applyStatusValue(r.Status)); err != nil {
		return nil, s.logErr("PutApplyStatus", err)
	}
	return &pb.PutApplyStatusResponse{}, nil
}

func (s *server) ListApplyStatus(ctx context.Context, _ *pb.ListApplyStatusRequest) (*pb.ListApplyStatusResponse, error) {
	as, err := s.applyStatus()
	if err != nil {
		return nil, err
	}
	statuses, err := as.ListApplyStatus(ctx)
	if err != nil {
		return nil, s.logErr("ListApplyStatus", err)
	}
	out := make([]*pb.ApplyStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, applyStatusProto(status))
	}
	return &pb.ListApplyStatusResponse{Statuses: out}, nil
}

func (s *server) configSnapshots() (storecontract.ConfigSnapshotStore, error) {
	if s.deps.ConfigSnapshots == nil {
		return nil, errConfigSnapshotsUnavailable
	}
	return s.deps.ConfigSnapshots, nil
}

func (s *server) PutConfigSnapshot(ctx context.Context, r *pb.PutConfigSnapshotRequest) (*pb.PutConfigSnapshotResponse, error) {
	cs, err := s.configSnapshots()
	if err != nil {
		return nil, err
	}
	if err := cs.PutConfigSnapshot(ctx, configSnapshotValue(r.Snapshot)); err != nil {
		return nil, s.logErr("PutConfigSnapshot", err)
	}
	return &pb.PutConfigSnapshotResponse{}, nil
}

func (s *server) GetConfigSnapshot(ctx context.Context, _ *pb.GetConfigSnapshotRequest) (*pb.GetConfigSnapshotResponse, error) {
	cs, err := s.configSnapshots()
	if err != nil {
		return nil, err
	}
	snapshot, found, err := cs.ConfigSnapshot(ctx)
	if err != nil {
		return nil, s.logErr("GetConfigSnapshot", err)
	}
	if !found {
		return &pb.GetConfigSnapshotResponse{}, nil
	}
	return &pb.GetConfigSnapshotResponse{Snapshot: configSnapshotProto(snapshot), Found: true}, nil
}

func (s *server) channelStatuses() (storecontract.ChannelStatusStore, error) {
	if s.deps.ChannelStatus == nil {
		return nil, errChannelStatusUnavailable
	}
	return s.deps.ChannelStatus, nil
}

func (s *server) PutChannelStatus(ctx context.Context, r *pb.PutChannelStatusRequest) (*pb.PutChannelStatusResponse, error) {
	cs, err := s.channelStatuses()
	if err != nil {
		return nil, err
	}
	if err := cs.PutChannelStatus(ctx, channelStatusesValue(r.Channels)); err != nil {
		return nil, s.logErr("PutChannelStatus", err)
	}
	return &pb.PutChannelStatusResponse{}, nil
}

func (s *server) ListChannelStatus(ctx context.Context, _ *pb.ListChannelStatusRequest) (*pb.ListChannelStatusResponse, error) {
	cs, err := s.channelStatuses()
	if err != nil {
		return nil, err
	}
	channels, err := cs.ChannelStatus(ctx)
	if err != nil {
		return nil, s.logErr("ListChannelStatus", err)
	}
	return &pb.ListChannelStatusResponse{Channels: channelStatusesProto(channels)}, nil
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

func (s *server) mapping() (storecontract.MappingStore, error) {
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

func (s *server) RecordMappingMatch(ctx context.Context, r *pb.RecordMappingMatchRequest) (*pb.RecordMappingMatchResponse, error) {
	if s.deps.MappingMatches == nil {
		return nil, status.Error(codes.Unavailable, "mapping match recorder unavailable")
	}
	if err := s.deps.MappingMatches.RecordMappingMatch(ctx, r.MappingId, r.CaptureId); err != nil {
		return nil, s.logErr("RecordMappingMatch", err)
	}
	return &pb.RecordMappingMatchResponse{}, nil
}

// Binding

func (s *server) binding() (storecontract.BindingStore, error) {
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

// Source

func (s *server) sources() (storecontract.SourceStore, error) {
	if s.deps.Sources == nil {
		return nil, errSourceUnavailable
	}
	return s.deps.Sources, nil
}

func (s *server) InsertSource(ctx context.Context, r *pb.InsertSourceRequest) (*pb.InsertSourceResponse, error) {
	ss, err := s.sources()
	if err != nil {
		return nil, err
	}
	if err := ss.InsertSource(ctx, sourceValue(r.Source)); err != nil {
		return nil, s.logErr("InsertSource", err)
	}
	return &pb.InsertSourceResponse{}, nil
}

func (s *server) GetSource(ctx context.Context, r *pb.GetSourceRequest) (*pb.GetSourceResponse, error) {
	ss, err := s.sources()
	if err != nil {
		return nil, err
	}
	src, err := ss.GetSource(ctx, r.Path)
	if err != nil {
		return nil, s.logErr("GetSource", err)
	}
	if src == nil {
		return &pb.GetSourceResponse{}, nil
	}
	return &pb.GetSourceResponse{Source: sourceProto(*src), Found: true}, nil
}

func (s *server) ListSources(ctx context.Context, _ *pb.ListSourcesRequest) (*pb.ListSourcesResponse, error) {
	ss, err := s.sources()
	if err != nil {
		return nil, err
	}
	list, err := ss.ListSources(ctx)
	if err != nil {
		return nil, s.logErr("ListSources", err)
	}
	return &pb.ListSourcesResponse{Sources: mapValues(list, sourceProto)}, nil
}

func (s *server) SetSourceSigning(ctx context.Context, r *pb.SetSourceSigningRequest) (*pb.SetSourceSigningResponse, error) {
	ss, err := s.sources()
	if err != nil {
		return nil, err
	}
	if err := ss.SetSourceSigning(ctx, r.Path, source.Signing(r.From), source.Signing(r.To)); err != nil {
		return nil, s.logErr("SetSourceSigning", err)
	}
	return &pb.SetSourceSigningResponse{}, nil
}

func (s *server) SetSourceSecret(ctx context.Context, r *pb.SetSourceSecretRequest) (*pb.SetSourceSecretResponse, error) {
	ss, err := s.sources()
	if err != nil {
		return nil, err
	}
	if err := ss.SetSourceSecret(ctx, r.Path, r.Secret); err != nil {
		return nil, s.logErr("SetSourceSecret", err)
	}
	return &pb.SetSourceSecretResponse{}, nil
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

// Playbook dispatch

func (s *server) RecordPlaybookDispatch(ctx context.Context, r *pb.RecordPlaybookDispatchRequest) (*pb.RecordPlaybookDispatchResponse, error) {
	if s.deps.PlaybookDispatcher == nil {
		return nil, errPlaybookDispatcherUnavailable
	}
	if err := s.deps.PlaybookDispatcher.RecordPlaybookDispatch(ctx, r.PlaybookId, r.PlaybookVersion, r.EventId, r.ActionId); err != nil {
		return nil, s.logErr("RecordPlaybookDispatch", err)
	}
	return &pb.RecordPlaybookDispatchResponse{}, nil
}

func (s *server) DeletePlaybookDispatches(ctx context.Context, r *pb.DeletePlaybookDispatchesRequest) (*pb.DeletePlaybookDispatchesResponse, error) {
	if s.deps.PlaybookDispatcher == nil {
		return nil, errPlaybookDispatcherUnavailable
	}
	if err := s.deps.PlaybookDispatcher.DeletePlaybookDispatches(ctx, r.PlaybookId); err != nil {
		return nil, s.logErr("DeletePlaybookDispatches", err)
	}
	return &pb.DeletePlaybookDispatchesResponse{}, nil
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
	inputs, err := task.DecodeInputs(r.InputsJson)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	t, err := s.deps.BindingTaskCreator.EnqueueBindingTask(ctx, r.Owner, r.Repo, r.Title, r.Body, r.Workflow, r.Identity, r.BindingId, int(r.BindingVersion), inputs)
	if err != nil {
		return nil, s.logErr("EnqueueBindingTask", err)
	}
	return &pb.EnqueueBindingTaskResponse{Task: taskProto(t)}, nil
}

// Task log

func (s *server) taskLogs() (storecontract.TaskLogStore, error) {
	if s.deps.TaskLogs == nil {
		return nil, errTaskLogsUnavailable
	}
	return s.deps.TaskLogs, nil
}

func (s *server) ReadTaskLog(ctx context.Context, r *pb.ReadTaskLogRequest) (*pb.ReadTaskLogResponse, error) {
	logs, err := s.taskLogs()
	if err != nil {
		return nil, err
	}
	page, err := logs.TaskLog(ctx, r.TaskId, int(r.Attempt), taskLogQueryValue(r))
	if err != nil {
		// A store that cannot read its own state directory keeps its own
		// identity: this is unavailability, not a missing log, and a caller
		// has to be able to tell them apart.
		if errors.Is(err, logging.ErrTaskLogsUnavailable) {
			return nil, errTaskLogsUnavailable
		}
		return nil, s.logErr("ReadTaskLog", err)
	}
	if !page.Found {
		return &pb.ReadTaskLogResponse{Attempt: r.Attempt}, nil
	}
	return &pb.ReadTaskLogResponse{
		Entries:    mapValues(page.Entries, taskLogEntryProto),
		Truncated:  page.Truncated,
		Attempt:    r.Attempt,
		Found:      true,
		Components: page.Components,
		File:       page.File,
	}, nil
}

// StreamTaskLogContent sends one attempt's log verbatim, one chunk per
// message, for a download. A single reply reports found=false with no chunks:
// the attempt has no log file, which is a normal state rather than a failure.
func (s *server) StreamTaskLogContent(r *pb.StreamTaskLogContentRequest, stream pb.StateStoreService_StreamTaskLogContentServer) error {
	logs, err := s.taskLogs()
	if err != nil {
		return err
	}
	// The chunking the client sees is owned by the reader that writes into
	// this buffer, so the wire never holds a whole log to re-slice it.
	buf := &chunkWriter{stream: stream, attempt: r.Attempt}
	found, err := logs.TaskLogContent(stream.Context(), r.TaskId, int(r.Attempt), buf)
	if err != nil {
		if errors.Is(err, logging.ErrTaskLogsUnavailable) {
			return errTaskLogsUnavailable
		}
		return s.logErr("StreamTaskLogContent", err)
	}
	if err := buf.flush(); err != nil {
		return err
	}
	// A found log with no bytes still reports found: an attempt whose file is
	// empty did produce a log, and found is what tells the download it may
	// write an empty file rather than answer 404.
	if found && buf.sent {
		return nil
	}
	return stream.Send(&pb.StreamTaskLogContentResponse{Attempt: r.Attempt, Found: found})
}

// chunkWriter packs everything a log reader writes into stream-sized messages
// on its way through. It exists so the read can be one io.CopyBuffer of the
// file into this writer: the reader keeps its own chunk size, and the bytes
// are never all held at once.
type chunkWriter struct {
	stream  pb.StateStoreService_StreamTaskLogContentServer
	attempt int64
	pending []byte
	// sent records whether any chunk reached the client, which is how the
	// caller tells "the file was empty" from "the file was read".
	sent bool
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for len(w.pending) >= taskLogChunkBytes {
		if err := w.emit(w.pending[:taskLogChunkBytes]); err != nil {
			return 0, err
		}
		w.pending = w.pending[taskLogChunkBytes:]
	}
	return len(p), nil
}

func (w *chunkWriter) emit(chunk []byte) error {
	w.sent = true
	// The slice is copied into the message: the reader's buffer is reused
	// across writes and must not be aliased by a message in flight.
	return w.stream.Send(&pb.StreamTaskLogContentResponse{
		Chunk:   bytes.Clone(chunk),
		Attempt: w.attempt,
		Found:   true,
	})
}

func (w *chunkWriter) flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	chunk := w.pending
	w.pending = nil
	return w.emit(chunk)
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
