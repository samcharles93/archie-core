package staterpc

import (
	"context"
	"errors"
	"io"
	"time"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// Client is the single remote adapter wrapping one StateStoreServiceClient,
// asserted against whichever narrow Go interfaces its caller needs -- exactly
// as gatewayrpc.Client asserts both gateway.ChatContract and
// gateway.SessionStore. See docs/prds/state-store-contract.md §2.
type Client struct {
	client pb.StateStoreServiceClient
}

// NewClient wraps conn's generated client. Close is a no-op: the store
// service owns its own DB lifecycle (§11).
func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{client: pb.NewStateStoreServiceClient(conn)}
}

// Close is a no-op; the remote store service owns its own DB lifecycle.
func (c *Client) Close() error { return nil }

var (
	_ workflow.Store           = (*Client)(nil)
	_ store.TaskStore          = (*Client)(nil)
	_ store.CaptureStore       = (*Client)(nil)
	_ store.MappingStore       = (*Client)(nil)
	_ store.BindingStore       = (*Client)(nil)
	_ store.BindingDispatcher  = (*Client)(nil)
	_ store.BindingTaskCreator = (*Client)(nil)
)

// Lifecycle

func (c *Client) EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) (bool, error) {
	r, err := c.client.EnqueueIssue(ctx, &pb.EnqueueIssueRequest{Owner: owner, Repo: repo, Number: int64(number), Title: title, Body: body, Labels: labels, Identity: identity})
	if err != nil {
		return false, unmapError(err)
	}
	return r.Inserted, nil
}

func (c *Client) EnqueueChatTask(ctx context.Context, owner, repo, title, body, wf, identity string) (*workflow.Task, error) {
	r, err := c.client.EnqueueChatTask(ctx, &pb.EnqueueChatTaskRequest{Owner: owner, Repo: repo, Title: title, Body: body, Workflow: wf, Identity: identity})
	if err != nil {
		return nil, unmapError(err)
	}
	return taskValue(r.Task), nil
}

func (c *Client) ClaimNext(ctx context.Context) (*workflow.Task, error) {
	r, err := c.client.ClaimNext(ctx, &pb.ClaimNextRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	if !r.Found {
		return nil, nil
	}
	return taskValue(r.Task), nil
}

func (c *Client) ClaimByIssue(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	r, err := c.client.ClaimByIssue(ctx, &pb.ClaimByIssueRequest{Owner: owner, Repo: repo, Number: int64(number)})
	if err != nil {
		return nil, unmapError(err)
	}
	if !r.Found {
		return nil, nil
	}
	return taskValue(r.Task), nil
}

func (c *Client) Transition(ctx context.Context, taskID int64, from, to, detail string) error {
	_, err := c.client.Transition(ctx, &pb.TransitionRequest{TaskId: taskID, From: from, To: to, Detail: detail})
	return unmapError(err)
}

func (c *Client) Update(ctx context.Context, t *workflow.Task) error {
	_, err := c.client.Update(ctx, &pb.UpdateRequest{Task: taskProto(t)})
	return unmapError(err)
}

func (c *Client) Requeue(ctx context.Context, taskID int64, fromStatus, wf string) error {
	_, err := c.client.Requeue(ctx, &pb.RequeueRequest{TaskId: taskID, FromStatus: fromStatus, Workflow: wf})
	return unmapError(err)
}

func (c *Client) RecoverStale(ctx context.Context) (int64, error) {
	r, err := c.client.RecoverStale(ctx, &pb.RecoverStaleRequest{})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Count, nil
}

// Archive / Retry

func (c *Client) ArchiveTask(ctx context.Context, taskID int64, fromStatus string, audit events.Event) (int64, error) {
	r, err := c.client.ArchiveTask(ctx, &pb.ArchiveTaskRequest{TaskId: taskID, FromStatus: fromStatus, Audit: eventProto(audit)})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.EventId, nil
}

func (c *Client) RetryTask(ctx context.Context, taskID int64, fromStatus, wf string) error {
	_, err := c.client.RetryTask(ctx, &pb.RetryTaskRequest{TaskId: taskID, FromStatus: fromStatus, Workflow: wf})
	return unmapError(err)
}

// Queries

func (c *Client) TaskByIssue(ctx context.Context, owner, repo string, number int) (*workflow.Task, error) {
	r, err := c.client.TaskByIssue(ctx, &pb.TaskByIssueRequest{Owner: owner, Repo: repo, Number: int64(number)})
	if err != nil {
		return nil, unmapError(err)
	}
	if !r.Found {
		return nil, nil
	}
	return taskValue(r.Task), nil
}

func (c *Client) TaskByID(ctx context.Context, taskID int64) (*workflow.Task, error) {
	r, err := c.client.TaskByID(ctx, &pb.TaskByIDRequest{TaskId: taskID})
	if err != nil {
		return nil, unmapError(err)
	}
	if !r.Found {
		return nil, nil
	}
	return taskValue(r.Task), nil
}

func (c *Client) OpenPRs(ctx context.Context) ([]workflow.Task, error) {
	r, err := c.client.OpenPRs(ctx, &pb.OpenPRsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return derefTasks(r.Tasks), nil
}

func (c *Client) ClearTerminalTasks(ctx context.Context) (int64, error) {
	r, err := c.client.ClearTerminalTasks(ctx, &pb.ClearTerminalTasksRequest{})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Count, nil
}

func (c *Client) Tasks(ctx context.Context, limit int) ([]workflow.Task, error) {
	r, err := c.client.Tasks(ctx, &pb.TasksRequest{Limit: int64(limit)})
	if err != nil {
		return nil, unmapError(err)
	}
	return derefTasks(r.Tasks), nil
}

func (c *Client) StatusCounts(ctx context.Context) (map[string]int, error) {
	r, err := c.client.StatusCounts(ctx, &pb.StatusCountsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	out := make(map[string]int, len(r.Counts))
	for k, v := range r.Counts {
		out[k] = int(v)
	}
	return out, nil
}

func (c *Client) IncrementRetryCount(ctx context.Context, taskID int64) error {
	_, err := c.client.IncrementRetryCount(ctx, &pb.IncrementRetryCountRequest{TaskId: taskID})
	return unmapError(err)
}

// Events

func (c *Client) InsertEvent(ctx context.Context, e events.Event) (int64, error) {
	r, err := c.client.InsertEvent(ctx, &pb.InsertEventRequest{Event: eventProto(e)})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Id, nil
}

func (c *Client) EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error) {
	r, err := c.client.EventsSince(ctx, &pb.EventsSinceRequest{SinceId: sinceID, Limit: int64(limit)})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Events, eventValue), nil
}

func (c *Client) TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error) {
	r, err := c.client.TaskEvents(ctx, &pb.TaskEventsRequest{TaskId: taskID})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Events, eventValue), nil
}

func (c *Client) WorkflowStats(ctx context.Context) ([]store.WorkflowStat, error) {
	r, err := c.client.WorkflowStats(ctx, &pb.WorkflowStatsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Stats, workflowStatValue), nil
}

func (c *Client) StageStats(ctx context.Context) ([]store.StageStat, error) {
	r, err := c.client.StageStats(ctx, &pb.StageStatsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Stats, stageStatValue), nil
}

func (c *Client) TokensByDay(ctx context.Context, days int) ([]store.DayTokens, error) {
	r, err := c.client.TokensByDay(ctx, &pb.TokensByDayRequest{Days: int64(days)})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Tokens, dayTokensValue), nil
}

// Capture

func (c *Client) InsertCapture(ctx context.Context, ce store.CapturedEvent, retention time.Duration, maxEvents int) (int64, error) {
	r, err := c.client.InsertCapture(ctx, &pb.InsertCaptureRequest{Capture: capturedEventProto(ce), RetentionSeconds: int64(retention.Seconds()), MaxEvents: int64(maxEvents)})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Id, nil
}

// ListCaptures calls StreamCaptures, not the deprecated unary ListCaptures
// RPC: a batch of large capture bodies in one unary response can exceed
// gRPC's 4MiB message cap (docs/prds/state-store-contract.md).
func (c *Client) ListCaptures(ctx context.Context, limit int) ([]store.CapturedEvent, error) {
	stream, err := c.client.StreamCaptures(ctx, &pb.StreamCapturesRequest{Limit: int64(limit)})
	if err != nil {
		return nil, unmapError(err)
	}
	var captures []store.CapturedEvent
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return captures, nil
		}
		if err != nil {
			return nil, unmapError(err)
		}
		captures = append(captures, capturedEventValue(r.Capture))
	}
}

// Mapping

func (c *Client) InsertMapping(ctx context.Context, m mapping.Mapping) (int64, error) {
	r, err := c.client.InsertMapping(ctx, &pb.InsertMappingRequest{Mapping: mappingProto(m)})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Id, nil
}

func (c *Client) GetMapping(ctx context.Context, id int64) (*mapping.Mapping, error) {
	r, err := c.client.GetMapping(ctx, &pb.GetMappingRequest{Id: id})
	if err != nil {
		return nil, unmapError(err)
	}
	if !r.Found {
		return nil, nil
	}
	v := mappingValue(r.Mapping)
	return &v, nil
}

func (c *Client) ListMappings(ctx context.Context) ([]mapping.Mapping, error) {
	r, err := c.client.ListMappings(ctx, &pb.ListMappingsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Mappings, mappingValue), nil
}

func (c *Client) UpdateMapping(ctx context.Context, m mapping.Mapping) error {
	_, err := c.client.UpdateMapping(ctx, &pb.UpdateMappingRequest{Mapping: mappingProto(m)})
	return unmapError(err)
}

func (c *Client) DeleteMapping(ctx context.Context, id int64) error {
	_, err := c.client.DeleteMapping(ctx, &pb.DeleteMappingRequest{Id: id})
	return unmapError(err)
}

// Binding

func (c *Client) InsertBinding(ctx context.Context, b binding.Binding) (int64, error) {
	r, err := c.client.InsertBinding(ctx, &pb.InsertBindingRequest{Binding: bindingProto(b)})
	if err != nil {
		return 0, unmapError(err)
	}
	return r.Id, nil
}

func (c *Client) GetBinding(ctx context.Context, id int64) (*binding.Binding, error) {
	r, err := c.client.GetBinding(ctx, &pb.GetBindingRequest{Id: id})
	if err != nil {
		return nil, unmapError(err)
	}
	if !r.Found {
		return nil, nil
	}
	v := bindingValue(r.Binding)
	return &v, nil
}

func (c *Client) ListBindings(ctx context.Context) ([]binding.Binding, error) {
	r, err := c.client.ListBindings(ctx, &pb.ListBindingsRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Bindings, bindingValue), nil
}

func (c *Client) UpdateBinding(ctx context.Context, b binding.Binding) error {
	_, err := c.client.UpdateBinding(ctx, &pb.UpdateBindingRequest{Binding: bindingProto(b)})
	return unmapError(err)
}

func (c *Client) DeleteBinding(ctx context.Context, id int64) error {
	_, err := c.client.DeleteBinding(ctx, &pb.DeleteBindingRequest{Id: id})
	return unmapError(err)
}

func (c *Client) ApproveBinding(ctx context.Context, id int64) error {
	_, err := c.client.ApproveBinding(ctx, &pb.ApproveBindingRequest{Id: id})
	return unmapError(err)
}

// Dispatch

func (c *Client) ArmedBindingsForSource(ctx context.Context, source string) ([]binding.Binding, error) {
	r, err := c.client.ArmedBindingsForSource(ctx, &pb.ArmedBindingsForSourceRequest{Source: source})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.Bindings, bindingValue), nil
}

func (c *Client) RecordDispatch(ctx context.Context, bindingID, bindingVersion, captureID, taskID int64) error {
	_, err := c.client.RecordDispatch(ctx, &pb.RecordDispatchRequest{BindingId: bindingID, BindingVersion: bindingVersion, CaptureId: captureID, TaskId: taskID})
	return unmapError(err)
}

// ListUndispatchedCaptures calls StreamUndispatchedCaptures; see ListCaptures
// above.
func (c *Client) ListUndispatchedCaptures(ctx context.Context, sources []string, limit int) ([]store.CapturedEvent, error) {
	stream, err := c.client.StreamUndispatchedCaptures(ctx, &pb.StreamUndispatchedCapturesRequest{Sources: sources, Limit: int64(limit)})
	if err != nil {
		return nil, unmapError(err)
	}
	var captures []store.CapturedEvent
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return captures, nil
		}
		if err != nil {
			return nil, unmapError(err)
		}
		captures = append(captures, capturedEventValue(r.Capture))
	}
}

// BindingTaskCreator

func (c *Client) EnqueueBindingTask(ctx context.Context, owner, repo, title, body, wf, identity string, bindingID int64, bindingVersion int) (*workflow.Task, error) {
	r, err := c.client.EnqueueBindingTask(ctx, &pb.EnqueueBindingTaskRequest{Owner: owner, Repo: repo, Title: title, Body: body, Workflow: wf, Identity: identity, BindingId: bindingID, BindingVersion: int64(bindingVersion)})
	if err != nil {
		return nil, unmapError(err)
	}
	return taskValue(r.Task), nil
}

func derefTasks(in []*pb.Task) []workflow.Task {
	if in == nil {
		return nil
	}
	out := make([]workflow.Task, len(in))
	for i, t := range in {
		out[i] = *taskValue(t)
	}
	return out
}
