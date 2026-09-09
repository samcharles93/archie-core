package gatewayrpc

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
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

// storedProto renders a canonical record in its wire shape. The role stays
// off the wire: each side derives it from the owning session's bot identity
// (see messaging.RoleForSender), so pb.Message needs no role field.
func storedProto(m messaging.Message) *pb.Message {
	return &pb.Message{
		MessageId: string(m.ID),
		SourceId:  m.SourceID,
		ChannelId: m.ConversationID.ChannelID,
		ThreadId:  m.ConversationID.ThreadID,
		From:      m.Sender,
		Text:      m.Text,
		At:        timestamp(m.At),
	}
}

// storedValue is storedProto's inverse. Role is left unset: only a caller
// that knows the owning session can derive it (see addressRecords).
func storedValue(v *pb.Message) messaging.Message {
	if v == nil {
		return messaging.Message{}
	}
	return messaging.Message{
		ID:             messaging.MessageID(v.MessageId),
		SourceID:       v.SourceId,
		ConversationID: messaging.ConversationID{ChannelID: v.ChannelId, ThreadID: v.ThreadId},
		Sender:         v.From,
		Text:           v.Text,
		At:             timeValue(v.At),
	}
}

// inboundProto renders a channel message and its transport context in wire
// shape. Page rides along; the record's role does not, for the reason
// storedProto gives.
func inboundProto(v messaging.Inbound) *pb.Message {
	m := storedProto(v.Message)
	m.Page = v.Page
	return m
}

// inboundValue reconstructs a channel message from the wire. Role is
// RoleUser without consulting the session: this is a message a channel
// frontend is delivering on a person's behalf, which is the only kind of
// message Route and Stream accept.
func inboundValue(v *pb.Message) messaging.Inbound {
	if v == nil {
		return messaging.Inbound{}
	}
	msg := storedValue(v)
	msg.Role = messaging.RoleUser
	return messaging.Inbound{Message: msg, Page: v.Page}
}

func toolProto(v messaging.ToolCallEvent) *pb.ToolCall {
	return &pb.ToolCall{
		Id:         v.ID,
		Name:       v.Name,
		Parameters: v.Parameters,
		Output:     v.Output,
		Error:      v.Err,
	}
}

func toolValue(v *pb.ToolCall) messaging.ToolCallEvent {
	if v == nil {
		return messaging.ToolCallEvent{}
	}
	return messaging.ToolCallEvent{
		ID:         v.Id,
		Name:       v.Name,
		Parameters: v.Parameters,
		Output:     v.Output,
		Err:        v.Error,
	}
}

func turnProto(v messaging.TurnRecord) *pb.Turn {
	return &pb.Turn{
		TurnId:             v.TurnID,
		SessionId:          v.SessionID,
		SourceId:           v.SourceID,
		OwnerId:            v.OwnerID,
		InputMessageId:     v.InputMessageID,
		AssistantMessageId: v.AssistantMessageID,
		PartialText:        v.PartialText,
		ResponseText:       v.ResponseText,
		Error:              v.Error, CreatedAt: timestamp(v.CreatedAt),
		UpdatedAt: timestamp(v.UpdatedAt), Status: string(v.Status), Attempt: int64(v.Attempt), ToolCalls: mapValues(v.ToolCalls, toolProto),
	}
}

func turnValue(v *pb.Turn) messaging.TurnRecord {
	if v == nil {
		return messaging.TurnRecord{}
	}
	return messaging.TurnRecord{
		TurnID:             v.TurnId,
		SessionID:          v.SessionId,
		SourceID:           v.SourceId,
		OwnerID:            v.OwnerId,
		InputMessageID:     v.InputMessageId,
		AssistantMessageID: v.AssistantMessageId,
		PartialText:        v.PartialText,
		ResponseText:       v.ResponseText,
		Error:              v.Error, CreatedAt: timeValue(v.CreatedAt),
		UpdatedAt: timeValue(v.UpdatedAt), Status: messaging.TurnStatus(v.Status), Attempt: int(v.Attempt), ToolCalls: mapValues(v.ToolCalls, toolValue),
	}
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

func sessionProto(v messaging.SessionContext) *pb.Session {
	return &pb.Session{SessionId: v.SessionID, Platform: v.Source.Platform, BotUser: v.Source.BotUser, ChannelId: v.Source.ChannelID, ThreadId: v.Source.ThreadID, Title: v.Title, ParentSessionId: v.ParentSessionID, BranchName: v.BranchName, CreatedAt: timestamp(v.CreatedAt), LastActiveAt: timestamp(v.LastActiveAt)}
}

func sessionValue(v *pb.Session) messaging.SessionContext {
	if v == nil {
		return messaging.SessionContext{}
	}
	return messaging.SessionContext{SessionID: v.SessionId, Source: messaging.SessionSource{Platform: v.Platform, BotUser: v.BotUser, ChannelID: v.ChannelId, ThreadID: v.ThreadId}, Title: v.Title, ParentSessionID: v.ParentSessionId, BranchName: v.BranchName, CreatedAt: timeValue(v.CreatedAt), LastActiveAt: timeValue(v.LastActiveAt)}
}

func int64Pointer(v *int) *int64 {
	if v == nil {
		return nil
	}
	n := int64(*v)
	return &n
}

func intPointer(v *int64) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func mediaProto(v messaging.MediaEvent) *pb.Media {
	a := v.Attachment
	return &pb.Media{ToolName: v.ToolName, Type: a.Type, FileId: a.FileID, Url: a.URL, Path: a.Path, MimeType: a.MIMEType, FileName: a.FileName, FileSize: a.FileSize, Width: int64Pointer(a.Width), Height: int64Pointer(a.Height), Duration: int64Pointer(a.Duration)}
}

func mediaValue(v *pb.Media) messaging.MediaEvent {
	if v == nil {
		return messaging.MediaEvent{}
	}
	return messaging.MediaEvent{ToolName: v.ToolName, Attachment: messaging.MediaAttachment{Type: v.Type, FileID: v.FileId, URL: v.Url, Path: v.Path, MIMEType: v.MimeType, FileName: v.FileName, FileSize: v.FileSize, Width: intPointer(v.Width), Height: intPointer(v.Height), Duration: intPointer(v.Duration)}}
}

func eventProto(v messaging.ChatEvent) *pb.StreamResponse {
	return &pb.StreamResponse{Kind: v.Kind, Text: v.Text, SessionId: v.SessionID, Tool: toolProto(v.Tool), Media: mediaProto(v.Media)}
}

func eventValue(v *pb.StreamResponse) messaging.ChatEvent {
	return messaging.ChatEvent{Kind: v.Kind, Text: v.Text, SessionID: v.SessionId, Tool: toolValue(v.Tool), Media: mediaValue(v.Media)}
}

func snapshotProto(v messaging.ChatSnapshot) *pb.SnapshotResponse {
	groups := make(map[string]*pb.StringList, len(v.ModelsByProvider))
	for k, list := range v.ModelsByProvider {
		groups[k] = &pb.StringList{Values: list}
	}
	return &pb.SnapshotResponse{Sessions: mapValues(v.Sessions, sessionProto), Models: v.Models, ModelsByProvider: groups, Providers: v.Providers, ActiveModel: v.ActiveModel, ActiveProvider: v.ActiveProvider, Personas: v.Personas, ActivePersonas: v.ActivePersonas, RestartAvailable: v.RestartAvailable, CancellationAvailable: v.CancellationAvailable, PersonasAvailable: v.PersonasAvailable}
}

func snapshotValue(v *pb.SnapshotResponse) messaging.ChatSnapshot {
	groups := make(map[string][]string, len(v.ModelsByProvider))
	for k, list := range v.ModelsByProvider {
		groups[k] = list.GetValues()
	}
	return messaging.ChatSnapshot{Sessions: mapValues(v.Sessions, sessionValue), Models: v.Models, ModelsByProvider: groups, Providers: v.Providers, ActiveModel: v.ActiveModel, ActiveProvider: v.ActiveProvider, Personas: v.Personas, ActivePersonas: v.ActivePersonas, RestartAvailable: v.RestartAvailable, CancellationAvailable: v.CancellationAvailable, PersonasAvailable: v.PersonasAvailable}
}
