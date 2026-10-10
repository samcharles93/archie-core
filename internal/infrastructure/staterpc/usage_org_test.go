package staterpc

import (
	"context"
	"testing"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/usage"
)

type usageCapture struct{ got usage.Record }

func (u *usageCapture) RecordUsage(_ context.Context, r usage.Record) error { u.got = r; return nil }

// Chat usage is billed to the org of the principal the call was made for;
// a call made for nobody is the system org's.
func TestRecordUsageBillsTheCallersOrg(t *testing.T) {
	for _, tt := range []struct {
		name, caller, want string
	}{
		{"a principal's call", "acme", "acme"},
		{"a call for nobody", "", string(org.DefaultOrgID)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			capture := &usageCapture{}
			s := &server{deps: Deps{Usage: capture}}
			ctx := context.Background()
			if tt.caller != "" {
				ctx = org.WithOrg(ctx, org.OrgID(tt.caller))
			}
			if _, err := s.RecordUsage(ctx, &pb.RecordUsageRequest{Record: &pb.UsageRecord{Source: "chat", Provider: "openai", Model: "m"}}); err != nil {
				t.Fatal(err)
			}
			if capture.got.Org != tt.want {
				t.Fatalf("org = %q, want %q", capture.got.Org, tt.want)
			}
		})
	}
}
