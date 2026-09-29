package controlplane

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// orgRecordingClient is the rpc end that records the org of every request it
// is handed, so a test can read back what the client put on the wire.
type orgRecordingClient struct {
	queryOrg   string
	commandOrg string
	watchOrg   string
}

func (*orgRecordingClient) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	panic("Catalog is not part of the org-stamping path")
}

func (c *orgRecordingClient) Query(_ context.Context, in *pb.QueryRequest, _ ...grpc.CallOption) (*pb.QueryResponse, error) {
	c.queryOrg = in.GetOrgId()
	return &pb.QueryResponse{Resource: &pb.Resource{Kind: in.GetKind()}}, nil
}

func (*orgRecordingClient) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("History is not part of the org-stamping path")
}

func (*orgRecordingClient) Audit(context.Context, *pb.AuditRequest, ...grpc.CallOption) (*pb.AuditResponse, error) {
	return &pb.AuditResponse{}, nil
}

func (c *orgRecordingClient) Command(_ context.Context, in *pb.CommandRequest, _ ...grpc.CallOption) (*pb.CommandResponse, error) {
	c.commandOrg = in.GetOrgId()
	return &pb.CommandResponse{Resource: &pb.Resource{Version: 1}}, nil
}

func (c *orgRecordingClient) Watch(_ context.Context, in *pb.WatchRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	c.watchOrg = in.GetOrgId()
	return eofWatchStream{}, nil
}

// eofWatchStream is the client end of a stream that ends before it carries
// anything: the watch under test only builds the request.
type eofWatchStream struct{ grpc.ClientStream }

func (eofWatchStream) Recv() (*pb.WatchResponse, error) { return nil, io.EOF }

func TestClientStampsCallerOrgOnEveryRequestShape(t *testing.T) {
	// The table holds the org, not a context: a stored context.Context field is
	// what containedctx rejects, and each case only needs to say what the caller
	// put on its context.
	tests := []struct {
		name  string
		orgID string
		want  string
	}{
		{name: "no org on the context is the default org", want: string(org.DefaultOrgID)},
		{name: "org on the context travels on the wire", orgID: "acme", want: "acme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.orgID != "" {
				ctx = org.WithOrg(ctx, org.OrgID(tt.orgID))
			}
			rpc := &orgRecordingClient{}
			client := NewRPCClient(rpc)

			if _, _, err := client.Query(ctx, ChannelSettingsKind, func([]byte) error { return nil }); err != nil {
				t.Fatalf("Query: %v", err)
			}
			if _, err := client.ReplaceSchedules(ctx, nil, 0, "actor", "source", "request"); err != nil {
				t.Fatalf("ReplaceSchedules: %v", err)
			}
			if _, err := client.WatchResource(ctx, WorkflowExecutionSettingsKind, 0); err != nil {
				t.Fatalf("WatchResource: %v", err)
			}

			if rpc.queryOrg != tt.want || rpc.commandOrg != tt.want || rpc.watchOrg != tt.want {
				t.Fatalf("wire orgs = query %q command %q watch %q, want %q each",
					rpc.queryOrg, rpc.commandOrg, rpc.watchOrg, tt.want)
			}
		})
	}
}
