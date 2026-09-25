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
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/source"
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
		InputsJson: inputsJSON(t.Inputs),
		CreatedAt:  timestamp(t.CreatedAt), UpdatedAt: timestamp(t.UpdatedAt),
		ReviewPayload:             t.ReviewPayload,
		ReviewCursor:              t.ReviewCursor,
		ParkClass:                 t.ParkClass,
		RemediationRounds:         int32(t.RemediationRounds),
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
		Inputs:    inputsValue(t.InputsJson),
		CreatedAt: timeValue(t.CreatedAt), UpdatedAt: timeValue(t.UpdatedAt),
		ReviewPayload:             t.ReviewPayload,
		ReviewCursor:              t.ReviewCursor,
		ParkClass:                 t.ParkClass,
		RemediationRounds:         int(t.RemediationRounds),
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYAML:    t.WorkflowDefinitionYaml,
	}
}

// inputsJSON and inputsValue carry task inputs in task.EncodeInputs form.
// Both ends encode with EncodeInputs, so neither direction can fail on a
// value this contract produced.
func inputsJSON(inputs map[string]any) string {
	s, _ := task.EncodeInputs(inputs)
	return s
}

func inputsValue(s string) map[string]any {
	inputs, _ := task.DecodeInputs(s)
	return inputs
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
		ActorId: e.ActorID, ActorKind: e.ActorKind, PrincipalId: e.PrincipalID,
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
		ActorID: e.ActorId, ActorKind: e.ActorKind, PrincipalID: e.PrincipalId,
	}
}

func capturedEventProto(c storecontract.CapturedEvent) *pb.CapturedEvent {
	return &pb.CapturedEvent{
		Id: c.ID, ReceivedAt: timestamp(c.ReceivedAt), Source: c.Source,
		RemoteAddr: c.RemoteAddr, ContentType: c.ContentType, Headers: c.Headers,
		Body: c.Body, Authenticated: c.Authenticated, EventType: c.EventType,
		Unsigned: c.Unsigned,
	}
}

func capturedEventValue(c *pb.CapturedEvent) storecontract.CapturedEvent {
	if c == nil {
		return storecontract.CapturedEvent{}
	}
	return storecontract.CapturedEvent{
		ID: c.Id, ReceivedAt: timeValue(c.ReceivedAt), Source: c.Source,
		RemoteAddr: c.RemoteAddr, ContentType: c.ContentType, Headers: c.Headers,
		Body: c.Body, Authenticated: c.Authenticated, EventType: c.EventType,
		Unsigned: c.Unsigned,
	}
}

func sourceProto(s source.Source) *pb.Source {
	return &pb.Source{
		Path: s.Path, Signing: string(s.Signing), Secret: s.Secret,
		CreatedAt: timestamp(s.CreatedAt), UpdatedAt: timestamp(s.UpdatedAt),
	}
}

func sourceValue(s *pb.Source) source.Source {
	if s == nil {
		return source.Source{}
	}
	return source.Source{
		Path: s.Path, Signing: source.Signing(s.Signing), Secret: s.Secret,
		CreatedAt: timeValue(s.CreatedAt), UpdatedAt: timeValue(s.UpdatedAt),
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
		Id: m.ID, Name: m.Name, SourceHint: m.SourceHint, EventTypeId: m.EventTypeID,
		Fields:     mapValues(m.Fields, mappingFieldProto),
		MatchCount: m.MatchCount, LastMatchedAt: timestamp(m.LastMatchedAt),
		CreatedAt: timestamp(m.CreatedAt), UpdatedAt: timestamp(m.UpdatedAt),
	}
}

func mappingValue(m *pb.Mapping) mapping.Mapping {
	if m == nil {
		return mapping.Mapping{}
	}
	return mapping.Mapping{
		ID: m.Id, Name: m.Name, SourceHint: m.SourceHint, EventTypeID: m.EventTypeId,
		Fields:     mapValues(m.Fields, mappingFieldValue),
		MatchCount: m.MatchCount, LastMatchedAt: timeValue(m.LastMatchedAt),
		CreatedAt: timeValue(m.CreatedAt), UpdatedAt: timeValue(m.UpdatedAt),
	}
}

func bindingProto(b binding.Binding) *pb.Binding {
	return &pb.Binding{
		Id: b.ID, Name: b.Name, Matcher: &pb.BindingMatcher{Source: b.Matcher.Source},
		MappingId: b.MappingID, Filter: b.Filter, Workflow: b.Workflow, Owner: b.Owner, Repo: b.Repo,
		RepoParam: b.RepoParam, InputsJson: bindingInputsJSON(b.Inputs),
		Version: int64(b.Version), Status: string(b.Status),
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
		MappingID: b.MappingId, Filter: b.Filter, Workflow: b.Workflow, Owner: b.Owner, Repo: b.Repo,
		RepoParam: b.RepoParam, Inputs: bindingInputsValue(b.InputsJson),
		Version: int(b.Version), Status: binding.Status(b.Status),
		CreatedAt: timeValue(b.CreatedAt), UpdatedAt: timeValue(b.UpdatedAt),
	}
}

// bindingInputsJSON and bindingInputsValue carry a binding's input
// assignments as JSON. The domain type always marshals, and the client only
// decodes what the server marshalled.
func bindingInputsJSON(inputs map[string]binding.InputSource) string {
	if len(inputs) == 0 {
		return ""
	}
	data, _ := json.Marshal(inputs)
	return string(data)
}

func bindingInputsValue(s string) map[string]binding.InputSource {
	if s == "" {
		return nil
	}
	var inputs map[string]binding.InputSource
	_ = json.Unmarshal([]byte(s), &inputs)
	return inputs
}

func workflowStatProto(w storecontract.WorkflowStat) *pb.WorkflowStat {
	return &pb.WorkflowStat{
		Workflow: w.Workflow, Runs: int64(w.Runs), Merged: int64(w.Merged),
		Completed: int64(w.Completed), PrOpen: int64(w.PROpen), Parked: int64(w.Parked),
		AvgTokens: int64(w.AvgTokens), AvgSteps: w.AvgSteps, TotalTokens: int64(w.TotalToken),
	}
}

func workflowStatValue(w *pb.WorkflowStat) storecontract.WorkflowStat {
	if w == nil {
		return storecontract.WorkflowStat{}
	}
	return storecontract.WorkflowStat{
		Workflow: w.Workflow, Runs: int(w.Runs), Merged: int(w.Merged),
		Completed: int(w.Completed), PROpen: int(w.PrOpen), Parked: int(w.Parked),
		AvgTokens: int(w.AvgTokens), AvgSteps: w.AvgSteps, TotalToken: int(w.TotalTokens),
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
	msgEventTypeNotFound = "event type not found"
	msgEventTypeOverlap  = "event type overlap"
	msgEventTypeInvalid  = "event type invalid"
	msgBindingOverlap    = "binding overlap"
	msgBindingTransition = "binding transition rejected"
	msgAlreadyDispatched = "already dispatched"
	msgSourceNotFound    = "source not found"
	msgSourcePathTaken   = "source path taken"
	msgSourceSigning     = "source signing stale"
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
	for _, w := range wireErrors {
		if errors.Is(err, w.sentinel) {
			return status.Error(w.code, w.message)
		}
	}
	return status.Error(codes.Internal, msgInternal)
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
	return wireSentinels[wireStatus{st.Code(), st.Message()}]
}

type wireStatus struct {
	code    codes.Code
	message string
}

// wireErrors pairs each sentinel with its canonical (code, message). mapError
// reads it forward and sentinelForStatus backward, so the two directions
// cannot drift.
var wireErrors = []struct {
	sentinel error
	code     codes.Code
	message  string
}{
	{storecontract.ErrStaleTransition, codes.FailedPrecondition, msgStaleTransition},
	{storecontract.ErrBindingOverlap, codes.FailedPrecondition, msgBindingOverlap},
	{storecontract.ErrBindingTransition, codes.FailedPrecondition, msgBindingTransition},
	{storecontract.ErrSourceSigningStale, codes.FailedPrecondition, msgSourceSigning},
	{eventtype.ErrOverlap, codes.FailedPrecondition, msgEventTypeOverlap},
	{eventtype.ErrInvalid, codes.InvalidArgument, msgEventTypeInvalid},
	{storecontract.ErrBindingNotFound, codes.NotFound, msgBindingNotFound},
	{storecontract.ErrMappingNotFound, codes.NotFound, msgMappingNotFound},
	{storecontract.ErrEventTypeNotFound, codes.NotFound, msgEventTypeNotFound},
	{storecontract.ErrSourceNotFound, codes.NotFound, msgSourceNotFound},
	{storecontract.ErrAlreadyDispatched, codes.AlreadyExists, msgAlreadyDispatched},
	{storecontract.ErrSourcePathTaken, codes.AlreadyExists, msgSourcePathTaken},
	{logging.ErrTaskLogsUnavailable, codes.Unavailable, msgTaskLogsUnavailable},
}

// wireSentinels indexes wireErrors by (code, canonical message).
var wireSentinels = func() map[wireStatus]error {
	m := make(map[wireStatus]error, len(wireErrors))
	for _, w := range wireErrors {
		m[wireStatus{w.code, w.message}] = w.sentinel
	}
	return m
}()

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

// channelStatusesProto and channelStatusesValue carry the channel runtime state
// the hosting process reports. The state is a string rather than an enum on the
// wire: the vocabulary belongs to internal/channels/status, and a reader that
// does not know a state it receives must show it rather than reject the report.
func channelStatusesProto(channels []storecontract.ChannelStatus) []*pb.ChannelStatus {
	out := make([]*pb.ChannelStatus, 0, len(channels))
	for _, channel := range channels {
		out = append(out, &pb.ChannelStatus{
			Id:              channel.ID,
			Name:            channel.Name,
			State:           channel.State,
			Detail:          channel.Detail,
			Configured:      channel.Configured,
			ReloadSupported: channel.ReloadSupported,
			ObservedAt:      timestamp(channel.ObservedAt),
		})
	}
	return out
}

func channelStatusesValue(channels []*pb.ChannelStatus) []storecontract.ChannelStatus {
	out := make([]storecontract.ChannelStatus, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		out = append(out, storecontract.ChannelStatus{
			ID:              channel.Id,
			Name:            channel.Name,
			State:           channel.State,
			Detail:          channel.Detail,
			Configured:      channel.Configured,
			ReloadSupported: channel.ReloadSupported,
			ObservedAt:      timeValue(channel.ObservedAt),
		})
	}
	return out
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
