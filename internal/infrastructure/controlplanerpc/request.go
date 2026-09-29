package controlplanerpc

import (
	"context"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// Every control-plane request acts in one org's copy of a resource. The org
// travels on the request context (org.WithOrg), attached once by whatever
// established the caller's principal, and these constructors are the one place
// it is read back: a call site cannot send the wrong org, and a new call site
// cannot forget it. org.OrgFromContext yields the default org when the context
// carries none, so a process with no principal still acts where it always did.

// QueryRequest builds a resource query for the caller's org.
func QueryRequest(ctx context.Context, kind string) *pb.QueryRequest {
	return &pb.QueryRequest{Kind: kind, OrgId: string(org.OrgFromContext(ctx))}
}

// HistoryRequest builds an audit-history request for the caller's org.
func HistoryRequest(ctx context.Context, kind string) *pb.HistoryRequest {
	return &pb.HistoryRequest{Kind: kind, OrgId: string(org.OrgFromContext(ctx))}
}

// WatchRequest builds a resource watch for the caller's org.
func WatchRequest(ctx context.Context, kind string, afterVersion int64) *pb.WatchRequest {
	return &pb.WatchRequest{Kind: kind, AfterVersion: afterVersion, OrgId: string(org.OrgFromContext(ctx))}
}

// CommandRequest stamps the caller's org onto a command request the caller
// assembled, whose remaining fields differ per command.
func CommandRequest(ctx context.Context, request *pb.CommandRequest) *pb.CommandRequest {
	request.OrgId = string(org.OrgFromContext(ctx))
	return request
}
