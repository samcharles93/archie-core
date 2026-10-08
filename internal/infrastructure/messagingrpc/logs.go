package messagingrpc

import (
	"context"

	pb "github.com/samcharles93/archie-core/internal/contracts/messaging/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/logrpc"
	"github.com/samcharles93/archie-core/internal/logging"
)

func (s *server) RecentLogs(_ context.Context, r *pb.RecentLogsRequest) (*pb.RecentLogsResponse, error) {
	snapshot, err := logrpc.Encode(s.feed.Read(logrpc.Query(r.Query)))
	return &pb.RecentLogsResponse{Snapshot: snapshot}, err
}

func (c *Client) RecentLogs(ctx context.Context, q logging.Query) (logging.Result, error) {
	response, err := c.rpc.RecentLogs(ctx, &pb.RecentLogsRequest{Query: logrpc.Request(q)})
	return logrpc.Decode(response.GetSnapshot(), err)
}
