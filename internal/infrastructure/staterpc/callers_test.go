package staterpc

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

type principalsByID map[identity.IdentityID]org.OrgID

func (p principalsByID) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	return access.Principal{IdentityID: id, Org: p[id]}, nil
}

// TestCallerAttribution pins who a call acts as: the instance credential's
// metadata is trusted, a task grant's is not.
func TestCallerAttribution(t *testing.T) {
	const admin = "admin-token"
	taskToken := strings.Repeat("ab", 32)
	grants := &TaskGrants{}
	if err := grants.register(7, taskToken, 3600e9); err != nil {
		t.Fatal(err)
	}
	callers := Callers{Principals: principalsByID{"alice": "acme"}}
	update := &grpc.UnaryServerInfo{FullMethod: pb.StateStoreService_Update_FullMethodName}

	tests := []struct {
		name      string
		md        []string
		wantActor string
		wantOrg   org.OrgID
	}{
		{"service alone acts in the default org", []string{tokenMetadataKey, admin, callerMetadataKey, "archied"}, "archied", org.DefaultOrgID},
		{"forwarded principal sets actor and org", []string{tokenMetadataKey, admin, callerMetadataKey, "archie-ui", principalMetadataKey, "alice"}, "alice", "acme"},
		{"task grant cannot claim a principal", []string{tokenMetadataKey, taskToken, callerMetadataKey, "archie-ui", principalMetadataKey, "alice"}, "task/7", org.DefaultOrgID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(tt.md...))
			var gotActor string
			var gotOrg org.OrgID
			handler := func(ctx context.Context, _ any) (any, error) {
				gotActor, gotOrg = access.ActorFromContext(ctx), org.OrgFromContext(ctx)
				return nil, nil
			}
			chained := func(ctx context.Context, req any) (any, error) {
				return callers.Unary()(ctx, req, update, handler)
			}
			req := &pb.UpdateRequest{Task: &pb.Task{Id: 7}}
			if _, err := grants.UnaryInterceptor(admin)(ctx, req, update, chained); err != nil {
				t.Fatal(err)
			}
			if gotActor != tt.wantActor || gotOrg != tt.wantOrg {
				t.Fatalf("got actor %q org %q, want %q %q", gotActor, gotOrg, tt.wantActor, tt.wantOrg)
			}
		})
	}
}
