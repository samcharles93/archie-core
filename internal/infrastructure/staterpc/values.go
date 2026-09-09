package staterpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timeValue(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

func mapValues[A, B any](in []A, f func(A) B) []B {
	if in == nil {
		return nil
	}
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func taskProto(t *workflow.Task) *pb.Task { //nolint:dupl // mirror-image field-by-field proto<->domain mapping; taskValue below reverses every assignment, so line-level duplication is unavoidable without reflection
	if t == nil {
		return nil
	}
	return &pb.Task{
		Id: t.ID, Owner: t.Owner, Repo: t.Repo, IssueNumber: int64(t.IssueNumber),
		Title: t.Title, Body: t.Body, Labels: t.Labels, Status: t.Status,
		Workflow: t.Workflow, Stage: t.Stage, Branch: t.Branch, Plan: t.Plan, Notes: t.Notes,
		PrNumber: int64(t.PRNumber), TokensUsed: int64(t.TokensUsed), Iterations: int64(t.Iterations),
		Attempt: int64(t.Attempt), ParkReason: t.ParkReason, RetryCount: int64(t.RetryCount),
		WatchCommentId: t.WatchCommentID, Source: t.Source, Identity: t.Identity,
		BindingId: t.BindingID, BindingVersion: int64(t.BindingVersion),
		CreatedAt: timestamp(t.CreatedAt), UpdatedAt: timestamp(t.UpdatedAt),
	}
}

func taskValue(t *pb.Task) *workflow.Task { //nolint:dupl // see taskProto above
	if t == nil {
		return nil
	}
	return &workflow.Task{
		ID: t.Id, Owner: t.Owner, Repo: t.Repo, IssueNumber: int(t.IssueNumber),
		Title: t.Title, Body: t.Body, Labels: t.Labels, Status: t.Status,
		Workflow: t.Workflow, Stage: t.Stage, Branch: t.Branch, Plan: t.Plan, Notes: t.Notes,
		PRNumber: int(t.PrNumber), TokensUsed: int(t.TokensUsed), Iterations: int(t.Iterations),
		Attempt: int(t.Attempt), ParkReason: t.ParkReason, RetryCount: int(t.RetryCount),
		WatchCommentID: t.WatchCommentId, Source: t.Source, Identity: t.Identity,
		BindingID: t.BindingId, BindingVersion: int(t.BindingVersion),
		CreatedAt: timeValue(t.CreatedAt), UpdatedAt: timeValue(t.UpdatedAt),
	}
}

// eventDataJSON and eventDataValue convert events.Event.Data (map[string]any)
// to/from a JSON object string, mirroring the store's own on-disk column.
func eventDataJSON(data map[string]any) string {
	if data == nil {
		return ""
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(b)
}

func eventDataValue(s string) map[string]any {
	if s == "" {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return nil
	}
	return data
}

func eventProto(e events.Event) *pb.Event {
	return &pb.Event{
		Id: e.ID, At: timestamp(e.At), Kind: e.Kind, TaskId: e.TaskID, Repo: e.Repo,
		Issue: int64(e.Issue), Workflow: e.Workflow, Stage: e.Stage, Detail: e.Detail,
		DataJson: eventDataJSON(e.Data),
	}
}

func eventValue(e *pb.Event) events.Event {
	if e == nil {
		return events.Event{}
	}
	return events.Event{
		ID: e.Id, At: timeValue(e.At), Kind: e.Kind, TaskID: e.TaskId, Repo: e.Repo,
		Issue: int(e.Issue), Workflow: e.Workflow, Stage: e.Stage, Detail: e.Detail,
		Data: eventDataValue(e.DataJson),
	}
}

func capturedEventProto(c store.CapturedEvent) *pb.CapturedEvent {
	return &pb.CapturedEvent{
		Id: c.ID, ReceivedAt: timestamp(c.ReceivedAt), Source: c.Source,
		RemoteAddr: c.RemoteAddr, ContentType: c.ContentType, Headers: c.Headers,
		Body: c.Body, Authenticated: c.Authenticated,
	}
}

func capturedEventValue(c *pb.CapturedEvent) store.CapturedEvent {
	if c == nil {
		return store.CapturedEvent{}
	}
	return store.CapturedEvent{
		ID: c.Id, ReceivedAt: timeValue(c.ReceivedAt), Source: c.Source,
		RemoteAddr: c.RemoteAddr, ContentType: c.ContentType, Headers: c.Headers,
		Body: c.Body, Authenticated: c.Authenticated,
	}
}

func mappingFieldProto(f mapping.Field) *pb.MappingField {
	return &pb.MappingField{Name: f.Name, Path: f.Path, Type: string(f.Type), Required: f.Required}
}

func mappingFieldValue(f *pb.MappingField) mapping.Field {
	if f == nil {
		return mapping.Field{}
	}
	return mapping.Field{Name: f.Name, Path: f.Path, Type: mapping.FieldType(f.Type), Required: f.Required}
}

func mappingProto(m mapping.Mapping) *pb.Mapping {
	return &pb.Mapping{
		Id: m.ID, Name: m.Name, SourceHint: m.SourceHint,
		Fields:    mapValues(m.Fields, mappingFieldProto),
		CreatedAt: timestamp(m.CreatedAt), UpdatedAt: timestamp(m.UpdatedAt),
	}
}

func mappingValue(m *pb.Mapping) mapping.Mapping {
	if m == nil {
		return mapping.Mapping{}
	}
	return mapping.Mapping{
		ID: m.Id, Name: m.Name, SourceHint: m.SourceHint,
		Fields:    mapValues(m.Fields, mappingFieldValue),
		CreatedAt: timeValue(m.CreatedAt), UpdatedAt: timeValue(m.UpdatedAt),
	}
}

func bindingProto(b binding.Binding) *pb.Binding {
	return &pb.Binding{
		Id: b.ID, Name: b.Name, Matcher: &pb.BindingMatcher{Source: b.Matcher.Source},
		MappingId: b.MappingID, Workflow: b.Workflow, Owner: b.Owner, Repo: b.Repo,
		Version: int64(b.Version), Status: string(b.Status), Secret: b.Secret,
		CreatedAt: timestamp(b.CreatedAt), UpdatedAt: timestamp(b.UpdatedAt),
	}
}

func bindingValue(b *pb.Binding) binding.Binding {
	if b == nil {
		return binding.Binding{}
	}
	var source string
	if b.Matcher != nil {
		source = b.Matcher.Source
	}
	return binding.Binding{
		ID: b.Id, Name: b.Name, Matcher: binding.Matcher{Source: source},
		MappingID: b.MappingId, Workflow: b.Workflow, Owner: b.Owner, Repo: b.Repo,
		Version: int(b.Version), Status: binding.Status(b.Status), Secret: b.Secret,
		CreatedAt: timeValue(b.CreatedAt), UpdatedAt: timeValue(b.UpdatedAt),
	}
}

func workflowStatProto(w store.WorkflowStat) *pb.WorkflowStat {
	return &pb.WorkflowStat{
		Workflow: w.Workflow, Runs: int64(w.Runs), Merged: int64(w.Merged),
		PrOpen: int64(w.PROpen), Parked: int64(w.Parked), AvgTokens: int64(w.AvgTokens),
		AvgSteps: w.AvgSteps, TotalTokens: int64(w.TotalToken),
	}
}

func workflowStatValue(w *pb.WorkflowStat) store.WorkflowStat {
	if w == nil {
		return store.WorkflowStat{}
	}
	return store.WorkflowStat{
		Workflow: w.Workflow, Runs: int(w.Runs), Merged: int(w.Merged),
		PROpen: int(w.PrOpen), Parked: int(w.Parked), AvgTokens: int(w.AvgTokens),
		AvgSteps: w.AvgSteps, TotalToken: int(w.TotalTokens),
	}
}

func stageStatProto(s store.StageStat) *pb.StageStat {
	return &pb.StageStat{Workflow: s.Workflow, Stage: s.Stage, Runs: int64(s.Runs), AvgMs: int64(s.AvgMs), Errors: int64(s.Errors)}
}

func stageStatValue(s *pb.StageStat) store.StageStat {
	if s == nil {
		return store.StageStat{}
	}
	return store.StageStat{Workflow: s.Workflow, Stage: s.Stage, Runs: int(s.Runs), AvgMs: int(s.AvgMs), Errors: int(s.Errors)}
}

func dayTokensProto(d store.DayTokens) *pb.DayTokens {
	return &pb.DayTokens{Day: d.Day, Tokens: int64(d.Tokens)}
}

func dayTokensValue(d *pb.DayTokens) store.DayTokens {
	if d == nil {
		return store.DayTokens{}
	}
	return store.DayTokens{Day: d.Day, Tokens: int(d.Tokens)}
}

// Canonical public phrases for sentinel errors (rev. 2c §7). These strings
// are the wire contract for sentinel identity -- the client rehydrates by
// matching (code, message), never by parsing free-form text.
const (
	msgStaleTransition   = "stale transition"
	msgBindingNotFound   = "binding not found"
	msgMappingNotFound   = "mapping not found"
	msgBindingOverlap    = "binding overlap"
	msgBindingTransition = "binding transition rejected"
	msgAlreadyDispatched = "already dispatched"
	msgInternal          = "state store: internal error"
)

// mapError converts a store sentinel error into a structured gRPC status
// carrying a short, public, stable message -- never the raw wrapped chain
// (which may contain SQL, provider detail, or secrets). Infra errors map to
// codes.Internal with a sanitised message; the caller is expected to log the
// full error server-side before calling mapError.
//
// A context cancellation/deadline raised by the store (or by a caller ctx that
// expires mid-call, §6) must retain its identity, not be folded into
// codes.Internal: gRPC-Go surfaces context.Canceled/DeadlineExceeded to the
// client only for those exact codes, and the agent's workflow consumer
// depends on errors.Is(err, context.DeadlineExceeded) to distinguish an
// interrupted stage from a failed one. So a context error maps to its own
// gRPC code (and unmapError rehydrates it back to the sentinel).
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, context.Canceled.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	}
	switch {
	case errors.Is(err, store.ErrStaleTransition):
		return status.Error(codes.FailedPrecondition, msgStaleTransition)
	case errors.Is(err, store.ErrBindingNotFound):
		return status.Error(codes.NotFound, msgBindingNotFound)
	case errors.Is(err, store.ErrMappingNotFound):
		return status.Error(codes.NotFound, msgMappingNotFound)
	case errors.Is(err, store.ErrBindingOverlap):
		return status.Error(codes.FailedPrecondition, msgBindingOverlap)
	case errors.Is(err, store.ErrBindingTransition):
		return status.Error(codes.FailedPrecondition, msgBindingTransition)
	case errors.Is(err, store.ErrAlreadyDispatched):
		return status.Error(codes.AlreadyExists, msgAlreadyDispatched)
	default:
		return status.Error(codes.Internal, msgInternal)
	}
}

// unmapError rehydrates a gRPC status error back to the store sentinel it
// came from, so a caller's errors.Is(err, store.ErrX) keeps working across
// the wire. A non-status error (e.g. a transport failure) is returned
// unchanged.
//
// The deadline/cancel identity must survive too (§6): the agent's
// deadlineStore bounds each Store call with context.WithTimeout, and the
// workflow consumer checks errors.Is(err, context.DeadlineExceeded) to
// decide whether a stage was interrupted by shutdown rather than failed
// (workflow.go). gRPC-Go surfaces an expired or cancelled context as a
// *status.Error whose code is DeadlineExceeded or Canceled, so we rehydrate
// those back to the standard context sentinels -- otherwise the consumer
// would (wrongly) park a task that was merely interrupted.
func unmapError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.Canceled:
		return context.Canceled
	case codes.DeadlineExceeded:
		return context.DeadlineExceeded
	case codes.FailedPrecondition:
		switch st.Message() {
		case msgStaleTransition:
			return store.ErrStaleTransition
		case msgBindingOverlap:
			return store.ErrBindingOverlap
		case msgBindingTransition:
			return store.ErrBindingTransition
		}
	case codes.NotFound:
		switch st.Message() {
		case msgBindingNotFound:
			return store.ErrBindingNotFound
		case msgMappingNotFound:
			return store.ErrMappingNotFound
		}
	case codes.AlreadyExists:
		if st.Message() == msgAlreadyDispatched {
			return store.ErrAlreadyDispatched
		}
	}
	return fmt.Errorf("state store: %s: %w", st.Message(), err)
}

// configSnapshotProto and configSnapshotValue carry the dashboard's
// configuration projection. document crosses as bytes, unread by either side
// of this hop: the store holds it and the page renders it.
func configSnapshotProto(snapshot store.ConfigSnapshot) *pb.ConfigSnapshot {
	return &pb.ConfigSnapshot{
		Schema:      snapshot.Schema,
		Document:    snapshot.Document,
		PublishedAt: timestamp(snapshot.PublishedAt),
	}
}

func configSnapshotValue(snapshot *pb.ConfigSnapshot) store.ConfigSnapshot {
	if snapshot == nil {
		return store.ConfigSnapshot{}
	}
	return store.ConfigSnapshot{
		Schema:      snapshot.Schema,
		Document:    snapshot.Document,
		PublishedAt: timeValue(snapshot.PublishedAt),
	}
}
