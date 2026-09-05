package gatewayrpc

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/gateway"
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

func messageProto(v gateway.Message) *pb.Message {
	return &pb.Message{
		MessageId: v.MessageID,
		SourceId:  v.SourceID,
		ChannelId: v.ChannelID,
		ThreadId:  v.ThreadID,
		From:      v.From,
		Text:      v.Text,
		Page:      v.Page, At: timestamp(v.At),
	}
}

func messageValue(v *pb.Message) gateway.Message {
	if v == nil {
		return gateway.Message{}
	}
	return gateway.Message{
		MessageID: v.MessageId,
		SourceID:  v.SourceId,
		ChannelID: v.ChannelId,
		ThreadID:  v.ThreadId,
		From:      v.From,
		Text:      v.Text,
		Page:      v.Page, At: timeValue(v.At),
	}
}

func toolProto(v gateway.ToolCallEvent) *pb.ToolCall {
	return &pb.ToolCall{
		Id:         v.ID,
		Name:       v.Name,
		Parameters: v.Parameters,
		Output:     v.Output,
		Error:      v.Err,
	}
}

func toolValue(v *pb.ToolCall) gateway.ToolCallEvent {
	if v == nil {
		return gateway.ToolCallEvent{}
	}
	return gateway.ToolCallEvent{
		ID:         v.Id,
		Name:       v.Name,
		Parameters: v.Parameters,
		Output:     v.Output,
		Err:        v.Error,
	}
}

func turnProto(v gateway.TurnRecord) *pb.Turn {
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

func turnValue(v *pb.Turn) gateway.TurnRecord {
	if v == nil {
		return gateway.TurnRecord{}
	}
	return gateway.TurnRecord{
		TurnID:             v.TurnId,
		SessionID:          v.SessionId,
		SourceID:           v.SourceId,
		OwnerID:            v.OwnerId,
		InputMessageID:     v.InputMessageId,
		AssistantMessageID: v.AssistantMessageId,
		PartialText:        v.PartialText,
		ResponseText:       v.ResponseText,
		Error:              v.Error, CreatedAt: timeValue(v.CreatedAt),
		UpdatedAt: timeValue(v.UpdatedAt), Status: gateway.TurnStatus(v.Status), Attempt: int(v.Attempt), ToolCalls: mapValues(v.ToolCalls, toolValue),
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

func sessionProto(v gateway.SessionContext) *pb.Session {
	return &pb.Session{SessionId: v.SessionID, Platform: v.Source.Platform, BotUser: v.Source.BotUser, ChannelId: v.Source.ChannelID, ThreadId: v.Source.ThreadID, Title: v.Title, ParentSessionId: v.ParentSessionID, BranchName: v.BranchName, CreatedAt: timestamp(v.CreatedAt), LastActiveAt: timestamp(v.LastActiveAt)}
}

func sessionValue(v *pb.Session) gateway.SessionContext {
	if v == nil {
		return gateway.SessionContext{}
	}
	return gateway.SessionContext{SessionID: v.SessionId, Source: gateway.SessionSource{Platform: v.Platform, BotUser: v.BotUser, ChannelID: v.ChannelId, ThreadID: v.ThreadId}, Title: v.Title, ParentSessionID: v.ParentSessionId, BranchName: v.BranchName, CreatedAt: timeValue(v.CreatedAt), LastActiveAt: timeValue(v.LastActiveAt)}
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

func mediaProto(v gateway.MediaEvent) *pb.Media {
	a := v.Attachment
	return &pb.Media{ToolName: v.ToolName, Type: a.Type, FileId: a.FileID, Url: a.URL, Path: a.Path, MimeType: a.MIMEType, FileName: a.FileName, FileSize: a.FileSize, Width: int64Pointer(a.Width), Height: int64Pointer(a.Height), Duration: int64Pointer(a.Duration)}
}

func mediaValue(v *pb.Media) gateway.MediaEvent {
	if v == nil {
		return gateway.MediaEvent{}
	}
	return gateway.MediaEvent{ToolName: v.ToolName, Attachment: gateway.MediaAttachment{Type: v.Type, FileID: v.FileId, URL: v.Url, Path: v.Path, MIMEType: v.MimeType, FileName: v.FileName, FileSize: v.FileSize, Width: intPointer(v.Width), Height: intPointer(v.Height), Duration: intPointer(v.Duration)}}
}

func eventProto(v gateway.ChatEvent) *pb.StreamResponse {
	return &pb.StreamResponse{Kind: v.Kind, Text: v.Text, SessionId: v.SessionID, Tool: toolProto(v.Tool), Media: mediaProto(v.Media)}
}

func eventValue(v *pb.StreamResponse) gateway.ChatEvent {
	return gateway.ChatEvent{Kind: v.Kind, Text: v.Text, SessionID: v.SessionId, Tool: toolValue(v.Tool), Media: mediaValue(v.Media)}
}

func snapshotProto(v gateway.ChatSnapshot) *pb.SnapshotResponse {
	groups := make(map[string]*pb.StringList, len(v.ModelsByProvider))
	for k, list := range v.ModelsByProvider {
		groups[k] = &pb.StringList{Values: list}
	}
	return &pb.SnapshotResponse{Sessions: mapValues(v.Sessions, sessionProto), Models: v.Models, ModelsByProvider: groups, Providers: v.Providers, ActiveModel: v.ActiveModel, ActiveProvider: v.ActiveProvider, Personas: v.Personas, ActivePersonas: v.ActivePersonas, RestartAvailable: v.RestartAvailable, CancellationAvailable: v.CancellationAvailable, PersonasAvailable: v.PersonasAvailable}
}

func snapshotValue(v *pb.SnapshotResponse) gateway.ChatSnapshot {
	groups := make(map[string][]string, len(v.ModelsByProvider))
	for k, list := range v.ModelsByProvider {
		groups[k] = list.GetValues()
	}
	return gateway.ChatSnapshot{Sessions: mapValues(v.Sessions, sessionValue), Models: v.Models, ModelsByProvider: groups, Providers: v.Providers, ActiveModel: v.ActiveModel, ActiveProvider: v.ActiveProvider, Personas: v.Personas, ActivePersonas: v.ActivePersonas, RestartAvailable: v.RestartAvailable, CancellationAvailable: v.CancellationAvailable, PersonasAvailable: v.PersonasAvailable}
}
