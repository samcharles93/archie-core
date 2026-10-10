package staterpc

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
)

// TestTaskCredentialCannotReplaceItsPin pins that an Update made with a
// task's own credential reaches the store without a workflow definition, so
// the stored pin (where the run's package authority is read from) stays. The
// workflow name still moves for a handoff; the daemon's credential re-pins.
func TestTaskCredentialCannotReplaceItsPin(t *testing.T) {
	taskToken := strings.Repeat("ab", 32)
	grants := &TaskGrants{Store: credentials{sha256.Sum256([]byte(taskToken)): 7}}
	intercept := grants.UnaryInterceptor("admin")
	tests := []struct {
		name, token, wantYAML string
	}{
		{"task credential", taskToken, ""},
		{"daemon credential", "admin", "yaml-b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(tokenMetadataKey, tt.token))
			req := &pb.UpdateRequest{Task: &pb.Task{
				Id: 7, Workflow: "handoff-target",
				WorkflowDefinitionVersion: 4, WorkflowDefinitionDigest: "digest-b", WorkflowDefinitionYaml: "yaml-b",
			}}
			var got *pb.Task
			_, err := intercept(ctx, req, &grpc.UnaryServerInfo{FullMethod: pb.StateStoreService_Update_FullMethodName},
				func(_ context.Context, r any) (any, error) {
					update, ok := r.(*pb.UpdateRequest)
					if !ok {
						t.Fatalf("handler got %T", r)
					}
					got = update.Task
					return &pb.UpdateResponse{}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if got.WorkflowDefinitionYaml != tt.wantYAML || (tt.wantYAML == "" && got.WorkflowDefinitionDigest != "") {
				t.Fatalf("definition reaching the store = %q/%q, want %q", got.WorkflowDefinitionYaml, got.WorkflowDefinitionDigest, tt.wantYAML)
			}
			if got.Workflow != "handoff-target" {
				t.Fatalf("workflow = %q, want handoff-target", got.Workflow)
			}
		})
	}
}
