package controlplane

import (
	"context"
	"slices"
	"testing"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type auditKeys struct {
	ResourceStore
	asked []string
}

func (a *auditKeys) Audit(_ context.Context, _ string, keys []string, _ int) ([]storecontract.AuditEntry, error) {
	a.asked = keys
	out := make([]storecontract.AuditEntry, 0, len(keys))
	for _, key := range keys {
		out = append(out, storecontract.AuditEntry{RecordKey: key})
	}
	return out, nil
}

// TestAuditStaysInCallerOrg pins that an audit request reads only the
// caller's org: kinds become that org's record keys, and a kind cannot spell
// another org's.
func TestAuditStaysInCallerOrg(t *testing.T) {
	tests := []struct {
		name     string
		org      org.OrgID
		kind     string
		wantKey  string
		rejected bool
	}{
		{"default org reads its own kind", org.DefaultOrgID, "workflows", "workflows", false},
		{"other org reads its own kind", "acme", "workflows", "acme/workflows", false},
		{"a slash cannot reach another org", org.DefaultOrgID, "acme/workflows", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &auditKeys{}
			ctx := org.WithOrg(context.Background(), tt.org)
			got, err := (&Server{store: store}).Audit(ctx, &pb.AuditRequest{Kinds: []string{tt.kind}})
			if tt.rejected {
				if err == nil || store.asked != nil {
					t.Fatalf("want rejection before the store, got err %v asked %v", err, store.asked)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(store.asked, []string{tt.wantKey}) || got.Entries[0].RecordKey != tt.kind {
				t.Fatalf("asked %v, returned %q", store.asked, got.Entries[0].RecordKey)
			}
		})
	}
}
