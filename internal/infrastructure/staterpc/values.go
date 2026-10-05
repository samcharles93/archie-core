package staterpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/taskstate"
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

// mapValues maps a slice, returning an empty non-nil slice for empty input.
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
		Workflow: t.Workflow, Branch: t.Branch, Plan: t.Plan, Notes: t.Notes,
		PrNumber: int64(t.PRNumber), TokensUsed: int64(t.TokensUsed), Iterations: int64(t.Iterations),
		Attempt: int64(t.Attempt), ParkReason: t.ParkReason, RetryCount: int64(t.RetryCount),
		RetryMode:  t.RetryMode,
		ResumeFrom: t.ResumeFrom, ResumeResults: t.ResumeResults,
		WatchCommentId: t.WatchCommentID, Source: t.Source, Identity: t.Identity,
		BindingId: t.BindingID, BindingVersion: int64(t.BindingVersion),
		InputsJson:  inputsJSON(t.Inputs),
		OutputsJson: outputsJSON(t.Outputs),
		CreatedAt:   timestamp(t.CreatedAt), UpdatedAt: timestamp(t.UpdatedAt),
		ReviewPayload:             t.ReviewPayload,
		ReviewGate:                t.ReviewGate,
		RereviewRounds:            int64(t.RereviewRounds),
		ReviewCursor:              t.ReviewCursor,
		ParkClass:                 t.ParkClass,
		RemediationRounds:         int32(t.RemediationRounds),
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYaml:    t.WorkflowDefinitionYAML,
		Org:                       string(t.Org),
		CallParentTaskId:          t.CallParentTaskID,
		CallDepth:                 int32(t.CallDepth),
	}
}

func taskValue(t *pb.Task) *task.Task { //nolint:dupl // see taskProto above
	if t == nil {
		return nil
	}
	return &task.Task{
		ID: t.Id, Owner: t.Owner, Repo: t.Repo, IssueNumber: int(t.IssueNumber),
		Title: t.Title, Body: t.Body, Labels: t.Labels, Status: t.Status,
		Workflow: t.Workflow, Branch: t.Branch, Plan: t.Plan, Notes: t.Notes,
		PRNumber: int(t.PrNumber), TokensUsed: int(t.TokensUsed), Iterations: int(t.Iterations),
		Attempt: int(t.Attempt), ParkReason: t.ParkReason, RetryCount: int(t.RetryCount),
		RetryMode:  t.RetryMode,
		ResumeFrom: t.ResumeFrom, ResumeResults: t.ResumeResults,
		WatchCommentID: t.WatchCommentId, Source: t.Source, Identity: t.Identity,
		BindingID: t.BindingId, BindingVersion: int(t.BindingVersion),
		Inputs:    inputsValue(t.InputsJson),
		Outputs:   outputsValue(t.OutputsJson),
		CreatedAt: timeValue(t.CreatedAt), UpdatedAt: timeValue(t.UpdatedAt),
		ReviewPayload:             t.ReviewPayload,
		ReviewGate:                t.ReviewGate,
		RereviewRounds:            int(t.RereviewRounds),
		ReviewCursor:              t.ReviewCursor,
		ParkClass:                 t.ParkClass,
		RemediationRounds:         int(t.RemediationRounds),
		WorkflowDefinitionVersion: t.WorkflowDefinitionVersion,
		WorkflowDefinitionDigest:  t.WorkflowDefinitionDigest,
		WorkflowDefinitionYAML:    t.WorkflowDefinitionYaml,
		Org:                       org.OrgID(t.Org),
		CallParentTaskID:          t.CallParentTaskId,
		CallDepth:                 int(t.CallDepth),
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

// outputsJSON and outputsValue carry a task's written outputs in
// task.EncodeOutputs form, exactly as inputsJSON/inputsValue carry inputs.
func outputsJSON(outputs map[string]any) string {
	s, _ := task.EncodeOutputs(outputs)
	return s
}

func outputsValue(s string) map[string]any {
	outputs, _ := task.DecodeOutputs(s)
	return outputs
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
		Path: s.Path, Name: s.Name, Signing: string(s.Signing), Secret: s.Secret,
		CreatedAt: timestamp(s.CreatedAt), UpdatedAt: timestamp(s.UpdatedAt),
	}
}

func sourceValue(s *pb.Source) source.Source {
	if s == nil {
		return source.Source{}
	}
	return source.Source{
		Path: s.Path, Name: s.Name, Signing: source.Signing(s.Signing), Secret: s.Secret,
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
		RepoParam: b.RepoParam, InputsJson: bindingInputsJSON(b.Inputs), OrgId: string(b.OrgID),
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
		RepoParam: b.RepoParam, Inputs: bindingInputsValue(b.InputsJson), OrgID: org.OrgID(b.OrgId),
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

func stepExecutionProto(s task.StepExecution) *pb.StepExecution {
	return &pb.StepExecution{
		Id: s.ID, ExecutionId: s.ExecutionID, Attempt: int64(s.Attempt),
		ParentId: s.ParentID, Depth: int32(s.Depth), Kind: s.Kind, Name: s.Name,
		Status: string(s.Status), Detail: s.Detail, TokensUsed: s.TokensUsed,
		StartedAt: timestamp(s.StartedAt), FinishedAt: timestamp(s.FinishedAt),
		CalledExecutionId: s.CalledExecutionID,
	}
}

func stepExecutionValue(s *pb.StepExecution) task.StepExecution {
	if s == nil {
		return task.StepExecution{}
	}
	return task.StepExecution{
		ID: s.Id, ExecutionID: s.ExecutionId, Attempt: int(s.Attempt),
		ParentID: s.ParentId, Depth: int(s.Depth), Kind: s.Kind, Name: s.Name,
		Status: taskstate.StepStatus(s.Status), Detail: s.Detail, TokensUsed: s.TokensUsed,
		StartedAt: timeValue(s.StartedAt), FinishedAt: timeValue(s.FinishedAt),
		CalledExecutionID: s.CalledExecutionId,
	}
}

func harnessSecretProto(s harnesssecret.Secret) *pb.HarnessSecret {
	return &pb.HarnessSecret{
		Org: s.Org, Service: s.Service, AccessToken: s.AccessToken,
		RefreshToken: s.RefreshToken, TokenType: s.TokenType,
		ExpiresAt: timestamp(s.ExpiresAt), UpdatedAt: timestamp(s.UpdatedAt),
		Scopes: s.Scopes,
	}
}

func harnessSecretValue(s *pb.HarnessSecret) harnesssecret.Secret {
	if s == nil {
		return harnesssecret.Secret{}
	}
	return harnesssecret.Secret{
		Org: s.Org, Service: s.Service, AccessToken: s.AccessToken,
		RefreshToken: s.RefreshToken, TokenType: s.TokenType,
		ExpiresAt: timeValue(s.ExpiresAt), UpdatedAt: timeValue(s.UpdatedAt),
		Scopes: slices.Clone(s.Scopes),
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

// Canonical public phrases for sentinel errors. These strings
// are the wire contract for sentinel identity -- the client rehydrates by
// matching (code, message), never by parsing free-form text.
const (
	msgStaleTransition       = "stale transition"
	msgIllegalTransition     = "illegal transition"
	msgInvalidStep           = "step execution invalid"
	msgBindingNotFound       = "binding not found"
	msgMappingNotFound       = "mapping not found"
	msgEventTypeNotFound     = "event type not found"
	msgEventTypeOverlap      = "event type overlap"
	msgEventTypeInvalid      = "event type invalid"
	msgBindingOverlap        = "binding overlap"
	msgBindingTransition     = "binding transition rejected"
	msgAlreadyDispatched     = "already dispatched"
	msgSourceNotFound        = "source not found"
	msgSourcePathTaken       = "source path taken"
	msgSourceSigning         = "source signing stale"
	msgHarnessSecretNotFound = "harness secret not found"
	// msgRereviewCapReached is the canonical message of
	// storecontract.ErrRereviewCapReached on the wire. Changing it breaks
	// errors.Is on the client without changing behaviour visibly.
	msgRereviewCapReached = "store: re-review cap reached"
	msgInternal           = "state store: internal error"
	// msgTaskLogsUnavailable is part of the wire contract for
	// ErrTaskLogsUnavailable.
	msgTaskLogsUnavailable = "task log reader unavailable"
)

// mapError converts a store sentinel to a gRPC status with a stable public
// message; other errors become Internal. Context errors keep their codes.
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

// unmapError turns a gRPC status back into its store sentinel, context error
// or ErrTaskLogsUnavailable. Non-status errors are returned unchanged.
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
// are part of the wire contract. A code whose message is not one this
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
	{access.ErrPolicyLockout, codes.FailedPrecondition, access.ErrPolicyLockout.Error()},
	{storecontract.ErrIllegalTransition, codes.FailedPrecondition, msgIllegalTransition},
	{storecontract.ErrInvalidStep, codes.InvalidArgument, msgInvalidStep},
	{storecontract.ErrResumeIncomplete, codes.FailedPrecondition, storecontract.ErrResumeIncomplete.Error()},
	{storecontract.ErrBindingOverlap, codes.FailedPrecondition, msgBindingOverlap},
	{storecontract.ErrBindingTransition, codes.FailedPrecondition, msgBindingTransition},
	{storecontract.ErrSourceSigningStale, codes.FailedPrecondition, msgSourceSigning},
	{eventtype.ErrOverlap, codes.FailedPrecondition, msgEventTypeOverlap},
	{eventtype.ErrInvalid, codes.InvalidArgument, msgEventTypeInvalid},
	{storecontract.ErrBindingNotFound, codes.NotFound, msgBindingNotFound},
	{storecontract.ErrMappingNotFound, codes.NotFound, msgMappingNotFound},
	{storecontract.ErrEventTypeNotFound, codes.NotFound, msgEventTypeNotFound},
	{storecontract.ErrSourceNotFound, codes.NotFound, msgSourceNotFound},
	{storecontract.ErrSourceInUse, codes.FailedPrecondition, storecontract.ErrSourceInUse.Error()},
	{storecontract.ErrAlreadyDispatched, codes.AlreadyExists, msgAlreadyDispatched},
	{storecontract.ErrCallNotYours, codes.PermissionDenied, storecontract.ErrCallNotYours.Error()},
	{storecontract.ErrCallCallerNotRunning, codes.FailedPrecondition, storecontract.ErrCallCallerNotRunning.Error()},
	{storecontract.ErrCallDepthExceeded, codes.FailedPrecondition, storecontract.ErrCallDepthExceeded.Error()},
	{storecontract.ErrSourcePathTaken, codes.AlreadyExists, msgSourcePathTaken},
	{storecontract.ErrHarnessSecretNotFound, codes.NotFound, msgHarnessSecretNotFound},
	{storecontract.ErrRereviewCapReached, codes.FailedPrecondition, msgRereviewCapReached},
	{storepkg.ErrNotFound, codes.NotFound, storepkg.ErrNotFound.Error()},
	{storepkg.ErrInstalled, codes.AlreadyExists, storepkg.ErrInstalled.Error()},
	{storepkg.ErrRequired, codes.FailedPrecondition, storepkg.ErrRequired.Error()},
	{storepkg.ErrAuthorityNotDeclared, codes.FailedPrecondition, storepkg.ErrAuthorityNotDeclared.Error()},
	{storepkg.ErrContributionCollision, codes.InvalidArgument, storepkg.ErrContributionCollision.Error()},
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

func presenceProto(presence storecontract.Presence) *pb.Presence {
	return &pb.Presence{
		Service:     presence.Service,
		InstanceId:  presence.InstanceID,
		Version:     presence.Version,
		InstallType: presence.InstallType,
		StartedAt:   timestamp(presence.StartedAt),
		ReportedAt:  timestamp(presence.ReportedAt),
		Ready:       presence.Ready,
		Detail:      presence.Detail,
	}
}

func presenceValue(presence *pb.Presence) storecontract.Presence {
	if presence == nil {
		return storecontract.Presence{}
	}
	return storecontract.Presence{
		Service:     presence.Service,
		InstanceID:  presence.InstanceId,
		Version:     presence.Version,
		InstallType: presence.InstallType,
		StartedAt:   timeValue(presence.StartedAt),
		ReportedAt:  timeValue(presence.ReportedAt),
		Ready:       presence.Ready,
		Detail:      presence.Detail,
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
		Since: timestamp(q.Since), Until: timestamp(q.Until), BeforeId: q.BeforeID,
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
		BeforeID:  r.BeforeId,
	}
}
