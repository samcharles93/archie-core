package gatewayrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type server struct {
	pb.UnimplementedChatServiceServer
	chat     gateway.ChatContract
	sessions gateway.SessionStore
}

func RegisterServer(registrar grpc.ServiceRegistrar, chat gateway.ChatContract, sessions ...gateway.SessionStore) {
	var ss gateway.SessionStore
	if len(sessions) > 0 {
		ss = sessions[0]
	}
	pb.RegisterChatServiceServer(registrar, &server{chat: chat, sessions: ss})
}

func (s *server) sessionStore() (gateway.SessionStore, error) {
	if s.sessions == nil {
		return nil, status.Error(codes.Unavailable, "session store unavailable")
	}
	return s.sessions, nil
}

func (s *server) Snapshot(ctx context.Context, _ *pb.SnapshotRequest) (*pb.SnapshotResponse, error) {
	v, err := s.chat.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return snapshotProto(v), nil
}

func (s *server) GetSession(ctx context.Context, r *pb.GetSessionRequest) (*pb.GetSessionResponse, error) {
	v, found, err := s.chat.GetSession(ctx, r.SessionId)
	if err != nil {
		return nil, err
	}
	return &pb.GetSessionResponse{Session: sessionProto(v), Found: found}, nil
}

func (s *server) RecentMessages(ctx context.Context, r *pb.RecentMessagesRequest) (*pb.RecentMessagesResponse, error) {
	v, err := s.chat.RecentMessages(ctx, r.SessionId, int(r.Limit))
	if err != nil {
		return nil, err
	}
	return &pb.RecentMessagesResponse{Messages: mapValues(v, messageProto)}, nil
}

func (s *server) RecentTurns(ctx context.Context, r *pb.RecentTurnsRequest) (*pb.RecentTurnsResponse, error) {
	v, err := s.chat.RecentTurns(ctx, r.SessionId, int(r.Limit))
	if err != nil {
		return nil, err
	}
	return &pb.RecentTurnsResponse{Turns: mapValues(v, turnProto)}, nil
}

func (s *server) Route(ctx context.Context, r *pb.RouteRequest) (*pb.RouteResponse, error) {
	v, err := s.chat.Route(ctx, messageValue(r.Message))
	if err != nil {
		return nil, err
	}
	return &pb.RouteResponse{Text: v.Text, SessionId: v.SessionID}, nil
}

func (s *server) Cancel(ctx context.Context, r *pb.CancelRequest) (*pb.CancelResponse, error) {
	v, err := s.chat.Cancel(ctx, r.SessionId)
	if err != nil {
		return nil, err
	}
	return &pb.CancelResponse{Cancelled: v.Cancelled, Dropped: int64(v.Dropped)}, nil
}

func (s *server) SetPersona(ctx context.Context, r *pb.SetPersonaRequest) (*pb.SetPersonaResponse, error) {
	v, err := s.chat.SetPersona(ctx, r.SessionId, r.Name)
	if err != nil {
		return nil, err
	}
	return &pb.SetPersonaResponse{Found: v}, nil
}

func (s *server) ApplyTaskAction(ctx context.Context, r *pb.ApplyTaskActionRequest) (*pb.ApplyTaskActionResponse, error) {
	v, err := s.chat.ApplyTaskAction(ctx, r.Identity, r.TaskId, taskstate.Action(r.Action))
	if err != nil {
		return nil, err
	}
	return &pb.ApplyTaskActionResponse{TaskId: v.TaskID, Action: v.Action, Message: v.Message}, nil
}

func (s *server) SaveSession(ctx context.Context, r *pb.SaveSessionRequest) (*pb.SaveSessionResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	return &pb.SaveSessionResponse{}, ss.Save(ctx, sessionValue(r.Session))
}

func (s *server) GetSessionsByChannel(ctx context.Context, r *pb.GetSessionsByChannelRequest) (*pb.GetSessionsByChannelResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	v, e := ss.GetByChannel(ctx, r.Platform, r.ChannelId)
	return &pb.GetSessionsByChannelResponse{Sessions: mapValues(v, sessionProto)}, e
}

func (s *server) DeleteSession(ctx context.Context, r *pb.DeleteSessionRequest) (*pb.DeleteSessionResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	return &pb.DeleteSessionResponse{}, ss.Delete(ctx, r.SessionId)
}

func (s *server) TouchSession(ctx context.Context, r *pb.TouchSessionRequest) (*pb.TouchSessionResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	return &pb.TouchSessionResponse{}, ss.Touch(ctx, r.SessionId)
}

func (s *server) ListSessions(ctx context.Context, _ *pb.ListSessionsRequest) (*pb.ListSessionsResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	v, e := ss.List(ctx)
	return &pb.ListSessionsResponse{Sessions: mapValues(v, sessionProto)}, e
}

func (s *server) SaveMessage(ctx context.Context, r *pb.SaveMessageRequest) (*pb.SaveMessageResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	return &pb.SaveMessageResponse{}, ss.SaveMessage(ctx, r.SessionId, messageValue(r.Message))
}

func (s *server) DeleteRecentMessages(ctx context.Context, r *pb.DeleteRecentMessagesRequest) (*pb.DeleteRecentMessagesResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	v, e := ss.DeleteRecentMessages(ctx, r.SessionId, int(r.Limit))
	return &pb.DeleteRecentMessagesResponse{Deleted: int64(v)}, e
}

func (s *server) MessageCount(ctx context.Context, r *pb.MessageCountRequest) (*pb.MessageCountResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	v, e := ss.MessageCount(ctx, r.SessionId)
	return &pb.MessageCountResponse{Count: int64(v)}, e
}

func (s *server) SaveMessages(ctx context.Context, r *pb.SaveMessagesRequest) (*pb.SaveMessagesResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	return &pb.SaveMessagesResponse{}, ss.SaveMessages(ctx, r.SessionId, mapValues(r.Messages, messageValue))
}

func (s *server) ReplaceMessages(ctx context.Context, r *pb.ReplaceMessagesRequest) (*pb.ReplaceMessagesResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	return &pb.ReplaceMessagesResponse{}, ss.ReplaceMessages(ctx, r.SessionId, mapValues(r.Messages, messageValue), r.Superseded)
}

func (s *server) SearchMessages(ctx context.Context, r *pb.SearchMessagesRequest) (*pb.SearchMessagesResponse, error) {
	ss, e := s.sessionStore()
	if e != nil {
		return nil, e
	}
	v, e := ss.SearchMessages(ctx, r.SessionId, gateway.MessageQuery{Query: r.Query, Limit: int(r.Limit), Offset: int(r.Offset)})
	return &pb.SearchMessagesResponse{Messages: mapValues(v.Messages, messageProto), NextOffset: int64(v.NextOffset), HasMore: v.HasMore, Truncated: v.Truncated}, e
}

func (s *server) Stream(r *pb.StreamRequest, out grpc.ServerStreamingServer[pb.StreamResponse]) error {
	events, err := s.chat.Stream(out.Context(), messageValue(r.Message))
	if err != nil {
		return err
	}
	for {
		select {
		case <-out.Context().Done():
			return out.Context().Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := out.Send(eventProto(event)); err != nil {
				return err
			}
		}
	}
}
