package staterpc

import (
	"testing"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
)

// A run credential records usage only against its own task, so one tenant's
// task cannot bill another's.
func TestRecordUsageAuthority(t *testing.T) {
	for _, tt := range []struct {
		name    string
		record  *pb.UsageRecord
		allowed bool
	}{
		{"own task", &pb.UsageRecord{TaskId: 7}, true},
		{"another task", &pb.UsageRecord{TaskId: 8}, false},
		{"no task", &pb.UsageRecord{OrgId: "org-sys"}, false},
		{"no record", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := &pb.RecordUsageRequest{Record: tt.record}
			if got := authorizesTaskScopedCall(pb.StateStoreService_RecordUsage_FullMethodName, req, 7); got != tt.allowed {
				t.Fatalf("authorized = %v, want %v", got, tt.allowed)
			}
		})
	}
}
