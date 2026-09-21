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
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
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

// mapValues maps a slice of proto values to domain values, always returning a
// non-nil result for empty input.
//
// Protobuf decodes an empty `repeated` field to nil, and Go marshals a nil
// slice to JSON null rather than []. Returning nil here made every decoded
// collection field's JSON type depend on whether it happened to have contents,
// and made the wire path disagree with the local path, which normalises its own
// slices. []B{} for empty input keeps the shape stable at every call site
// without making each one remember to guard.
func mapValues[A, B any](in []A, f func(A) B) []B {
	if len(in) == 0 {
		return []B{}
	}
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func taskProto(t *task.Task) *pb.Task { //nolint:dupl // mirror-image field-by-field proto<->domain mapping; taskValue below reverses every assignment, so line-level duplication is unavoidable without reflection
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
		ReviewPayload:             t.ReviewPayload,
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYaml:    t.WorkflowDefinitionYAML,
	}
}

func taskValue(t *pb.Task) *task.Task { //nolint:dupl // see taskProto above
	if t == nil {
		return nil
	}
	return &task.Task{
		ID: t.Id, Owner: t.Owner, Repo: t.Repo, IssueNumber: int(t.IssueNumber),
		Title: t.Title, Body: t.Body, Labels: t.Labels, Status: t.Status,
		Workflow: t.Workflow, Stage: t.Stage, Branch: t.Branch, Plan: t.Plan, Notes: t.Notes,
		PRNumber: int(t.PrNumber), TokensUsed: int(t.TokensUsed), Iterations: int(t.Iterations),
		Attempt: int(t.Attempt), ParkReason: t.ParkReason, RetryCount: int(t.RetryCount),
		WatchCommentID: t.WatchCommentId, Source: t.Source, Identity: t.Identity,
		BindingID: t.BindingId, BindingVersion: int(t.BindingVersion),
		CreatedAt: timeValue(t.CreatedAt), UpdatedAt: timeValue(t.UpdatedAt),
		ReviewPayload:             t.ReviewPayload,
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYAML:    t.WorkflowDefinitionYaml,
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
		return fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
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
		Issue: int64(e.Issue), Workflow: e.Workflow, Stage: e.Stage, Attempt: int64(e.Attempt),
		Detail: e.Detail, DataJson: eventDataJSON(e.Data),
	}
}

func eventValue(e *pb.Event) events.Event {
	if e == nil {
		return events.Event{}
	}
	return events.Event{
		ID: e.Id, At: timeValue(e.At), Kind: e.Kind, TaskID: e.TaskId, Repo: e.Repo,
		Issue: int(e.Issue), Workflow: e.Workflow, Stage: e.Stage, Attempt: int(e.Attempt),
		Detail: e.Detail, Data: eventDataValue(e.DataJson),
	}
}

func capturedEventProto(c storecontract.CapturedEvent) *pb.CapturedEvent {
	return &pb.CapturedEvent{
		Id: c.ID, ReceivedAt: timestamp(c.ReceivedAt), Source: c.Source,
		RemoteAddr: c.RemoteAddr, ContentType: c.ContentType, Headers: c.Headers,
		Body: c.Body, Authenticated: c.Authenticated,
	}
}

func capturedEventValue(c *pb.CapturedEvent) storecontract.CapturedEvent {
	if c == nil {
		return storecontract.CapturedEvent{}
	}
	return storecontract.CapturedEvent{
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

func workflowStatProto(w storecontract.WorkflowStat) *pb.WorkflowStat {
	return &pb.WorkflowStat{
		Workflow: w.Workflow, Runs: int64(w.Runs), Merged: int64(w.Merged),
		PrOpen: int64(w.PROpen), Parked: int64(w.Parked), AvgTokens: int64(w.AvgTokens),
		AvgSteps: w.AvgSteps, TotalTokens: int64(w.TotalToken),
	}
}

func workflowStatValue(w *pb.WorkflowStat) storecontract.WorkflowStat {
	if w == nil {
		return storecontract.WorkflowStat{}
	}
	return storecontract.WorkflowStat{
		Workflow: w.Workflow, Runs: int(w.Runs), Merged: int(w.Merged),
		PROpen: int(w.PrOpen), Parked: int(w.Parked), AvgTokens: int(w.AvgTokens),
		AvgSteps: w.AvgSteps, TotalToken: int(w.TotalTokens),
	}
}

func stageStatProto(s storecontract.StageStat) *pb.StageStat {
	return &pb.StageStat{Workflow: s.Workflow, Stage: s.Stage, Runs: int64(s.Runs), AvgMs: int64(s.AvgMs), Errors: int64(s.Errors)}
}

func stageStatValue(s *pb.StageStat) storecontract.StageStat {
	if s == nil {
		return storecontract.StageStat{}
	}
	return storecontract.StageStat{Workflow: s.Workflow, Stage: s.Stage, Runs: int(s.Runs), AvgMs: int(s.AvgMs), Errors: int(s.Errors)}
}

func dayTokensProto(d storecontract.DayTokens) *pb.DayTokens {
	return &pb.DayTokens{Day: d.Day, Tokens: int64(d.Tokens)}
}

func dayTokensValue(d *pb.DayTokens) storecontract.DayTokens {
	if d == nil {
		return storecontract.DayTokens{}
	}
	return storecontract.DayTokens{Day: d.Day, Tokens: int(d.Tokens)}
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
	// msgTaskLogsUnavailable is the public phrase for "this service has no
	// task-log reader". It is a wire contract like the sentinels above: the
	// client rehydrates logging.ErrTaskLogsUnavailable from (Unavailable, this
	// message), and a caller depends on that to tell "this process cannot read
	// logs" from "this attempt has no log".
	msgTaskLogsUnavailable = "task log reader unavailable"
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
	case errors.Is(err, storecontract.ErrStaleTransition):
		return status.Error(codes.FailedPrecondition, msgStaleTransition)
	case errors.Is(err, storecontract.ErrBindingNotFound):
		return status.Error(codes.NotFound, msgBindingNotFound)
	case errors.Is(err, storecontract.ErrMappingNotFound):
		return status.Error(codes.NotFound, msgMappingNotFound)
	case errors.Is(err, storecontract.ErrBindingOverlap):
		return status.Error(codes.FailedPrecondition, msgBindingOverlap)
	case errors.Is(err, storecontract.ErrBindingTransition):
		return status.Error(codes.FailedPrecondition, msgBindingTransition)
	case errors.Is(err, storecontract.ErrAlreadyDispatched):
		return status.Error(codes.AlreadyExists, msgAlreadyDispatched)
	default:
		return status.Error(codes.Internal, msgInternal)
	}
}

// unmapError rehydrates a gRPC status error back to the store sentinel it
// came from, so a caller's errors.Is(err, storecontract.ErrX) keeps working across
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
//
// Unavailable carries one message this package owns: the absent task-log
// reader. It is rehydrated for the same reason the error sentinels are --
// the dashboard's whole bug was rendering "this process cannot read logs" as
// "the attempt has no log", and only the typed error makes that distinction
// available to a caller.
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
	}
	if sentinel := sentinelForStatus(st); sentinel != nil {
		return sentinel
	}
	return fmt.Errorf("state store: %s: %w", st.Message(), err)
}

// sentinelForStatus maps a status to the store or logging sentinel it stands
// for, matched on (code, exact canonical message) -- those message constants
// are part of the wire contract (§4). A code whose message is not one this
// package defines returns nil, so the caller falls back to the wrapped form.
func sentinelForStatus(st *status.Status) error {
	switch st.Code() {
	case codes.FailedPrecondition:
		switch st.Message() {
		case msgStaleTransition:
			return storecontract.ErrStaleTransition
		case msgBindingOverlap:
			return storecontract.ErrBindingOverlap
		case msgBindingTransition:
			return storecontract.ErrBindingTransition
		}
	case codes.NotFound:
		switch st.Message() {
		case msgBindingNotFound:
			return storecontract.ErrBindingNotFound
		case msgMappingNotFound:
			return storecontract.ErrMappingNotFound
		}
	case codes.AlreadyExists:
		if st.Message() == msgAlreadyDispatched {
			return storecontract.ErrAlreadyDispatched
		}
	case codes.Unavailable:
		if st.Message() == msgTaskLogsUnavailable {
			return logging.ErrTaskLogsUnavailable
		}
	}
	return nil
}

// configSnapshotProto and configSnapshotValue carry the dashboard's
// configuration projection. document crosses as bytes, unread by either side
// of this hop: the store holds it and the page renders it.
func configSnapshotProto(snapshot storecontract.ConfigSnapshot) *pb.ConfigSnapshot {
	return &pb.ConfigSnapshot{
		Schema:      snapshot.Schema,
		Document:    snapshot.Document,
		PublishedAt: timestamp(snapshot.PublishedAt),
	}
}

func configSnapshotValue(snapshot *pb.ConfigSnapshot) storecontract.ConfigSnapshot {
	if snapshot == nil {
		return storecontract.ConfigSnapshot{}
	}
	return storecontract.ConfigSnapshot{
		Schema:      snapshot.Schema,
		Document:    snapshot.Document,
		PublishedAt: timeValue(snapshot.PublishedAt),
	}
}

func applyStatusProto(status storecontract.ApplyStatus) *pb.ApplyStatus {
	return &pb.ApplyStatus{
		Process:        status.Process,
		Kind:           status.Kind,
		AppliedVersion: status.AppliedVersion,
		Error:          status.Error,
		ReportedAt:     timestamp(status.ReportedAt),
	}
}

func applyStatusValue(status *pb.ApplyStatus) storecontract.ApplyStatus {
	if status == nil {
		return storecontract.ApplyStatus{}
	}
	return storecontract.ApplyStatus{
		Process:        status.Process,
		Kind:           status.Kind,
		AppliedVersion: status.AppliedVersion,
		Error:          status.Error,
		ReportedAt:     timeValue(status.ReportedAt),
	}
}

// taskLogEntryProto and taskLogEntryValue mirror internal/logging.Entry, whose
// Fields map crosses as a JSON object string exactly as events.Event.Data does
// (see eventDataJSON above). The logging package owns that format end to end;
// this is a transport of it, not a second definition.
func taskLogEntryProto(e logging.Entry) *pb.TaskLogEntry {
	return &pb.TaskLogEntry{
		Id: e.ID, Time: timestamp(e.Time), Level: e.Level, Msg: e.Message,
		FieldsJson: eventDataJSON(e.Fields),
	}
}

func taskLogEntryValue(e *pb.TaskLogEntry) logging.Entry {
	if e == nil {
		return logging.Entry{}
	}
	return logging.Entry{
		ID: e.Id, Time: timeValue(e.Time), Level: e.Level, Message: e.Msg,
		Fields: eventDataValue(e.FieldsJson),
	}
}

// taskLogRequestProto is the client half of the ReadTaskLog mapping and
// taskLogQueryValue the server half: the whole logging.Query crosses, not a
// subset of it, so a filter cannot silently stop working at the boundary.
func taskLogRequestProto(taskID int64, attempt int, q logging.Query) *pb.ReadTaskLogRequest {
	return &pb.ReadTaskLogRequest{
		TaskId: taskID, Attempt: int64(attempt), Limit: int64(q.Limit),
		Levels: q.Levels, Component: q.Component, Stage: q.Stage, Contains: q.Contains,
		Since: timestamp(q.Since), Until: timestamp(q.Until),
	}
}

func taskLogQueryValue(r *pb.ReadTaskLogRequest) logging.Query {
	return logging.Query{
		Levels:    r.Levels,
		Component: r.Component,
		Stage:     r.Stage,
		Contains:  r.Contains,
		Limit:     int(r.Limit),
		Since:     timeValue(r.Since),
		Until:     timeValue(r.Until),
	}
}
