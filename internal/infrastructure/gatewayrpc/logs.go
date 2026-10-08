package gatewayrpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/logrpc"
	"github.com/samcharles93/archie-core/internal/logging"
)

func (s *server) RecentLogs(ctx context.Context, r *pb.RecentLogsRequest) (*pb.RecentLogsResponse, error) {
	if s.catalog.Logs == nil {
		return nil, status.Error(codes.Unavailable, "service logs unavailable")
	}
	snapshot, err := logrpc.Encode(s.catalog.Logs(ctx, r.Service, logrpc.Query(r.Query)))
	return &pb.RecentLogsResponse{Snapshot: snapshot}, err
}

func (c *Client) RecentLogs(ctx context.Context, service string, q logging.Query) (logging.Result, error) {
	response, err := c.client.RecentLogs(ctx, &pb.RecentLogsRequest{Query: logrpc.Request(q), Service: service})
	return logrpc.Decode(response.GetSnapshot(), err)
}
