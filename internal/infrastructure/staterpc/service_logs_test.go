package staterpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
)

func TestServiceLogsRequireInstanceCredential(t *testing.T) {
	run := strings.Repeat("ab", 32)
	grants := &TaskGrants{Store: credentials{}}
	if err := grants.register(t.Context(), 7, run, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, token string
		code        codes.Code
	}{
		{"instance", "admin", codes.OK},
		{"task cannot read other services", run, codes.PermissionDenied},
		{"anonymous", "", codes.Unauthenticated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(tokenMetadataKey, tt.token))
			_, err := grants.UnaryInterceptor("admin")(ctx, &pb.RecentLogsRequest{}, &grpc.UnaryServerInfo{FullMethod: pb.StateStoreService_RecentLogs_FullMethodName}, func(context.Context, any) (any, error) { return &pb.RecentLogsResponse{}, nil })
			if status.Code(err) != tt.code {
				t.Fatalf("code %s want %s", status.Code(err), tt.code)
			}
		})
	}
}
