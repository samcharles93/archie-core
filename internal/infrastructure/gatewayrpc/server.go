package gatewayrpc

import (
	"context"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type server struct {
	pb.UnimplementedChatServiceServer
	chat gateway.ChatContract
}

func RegisterServer(registrar grpc.ServiceRegistrar, chat gateway.ChatContract) {
	pb.RegisterChatServiceServer(registrar, &server{chat: chat})
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
