package staterpc

import (
	"testing"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
)

func TestWorkerPresenceAuthority(t *testing.T) {
	for _, tt := range []struct {
		name, service, instance string
		allowed                 bool
	}{
		{"own task", "archie-agent", "7", true},
		{"another task", "archie-agent", "8", false},
		{"daemon", "archied", "7", false},
		{"broker", "nats", "7", false},
		{"noncanonical task", "archie-agent", "007", false},
		{"no task", "archie-agent", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := &pb.PutPresenceRequest{Presence: &pb.Presence{Service: tt.service, InstanceId: tt.instance}}
			if got := authorizesTaskScopedCall(pb.StateStoreService_PutPresence_FullMethodName, req, 7); got != tt.allowed {
				t.Fatalf("authorized = %v, want %v", got, tt.allowed)
			}
		})
	}
}
