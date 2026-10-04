package access

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

func TestAuthorizeDelivery(t *testing.T) {
	permitAll := access.Policy{ID: "all", Level: access.LevelInstance, Text: `permit(principal, action, resource);`}
	lanOnly := access.Policy{ID: "lan-only", Level: access.LevelInstance, Text: `forbid(principal, action == Archie::Action::"deliver", resource) unless { context.addr.isInRange(ip("10.0.0.0/8")) };`}
	sourceOnly := access.Policy{
		ID: "github-only", Level: access.LevelObject, OrgID: org.DefaultOrgID, ObjectKind: access.KindSource, ObjectID: "github",
		Text: `permit(principal, action == Archie::Action::"deliver", resource) when { context.addr.isInRange(ip("140.82.112.0/20")) };`,
	}
	shipped := access.ShippedOrgPolicies(org.DefaultOrgID)

	tests := []struct {
		name     string
		policies []access.Policy
		source   string
		addr     string
		allowed  bool
	}{
		{name: "no rules admits every sender", policies: shipped, source: "github", addr: "203.0.113.9", allowed: true},
		{name: "network rule admits its range", policies: append(shipped, permitAll, lanOnly), source: "ci", addr: "10.1.2.3", allowed: true},
		{name: "network rule refuses outside its range", policies: append(shipped, permitAll, lanOnly), source: "ci", addr: "203.0.113.9"},
		{name: "source policy refuses another sender", policies: append(shipped, sourceOnly), source: "github", addr: "203.0.113.9"},
		{name: "source policy admits its sender", policies: append(shipped, sourceOnly), source: "github", addr: "140.82.112.5", allowed: true},
		{name: "source policy leaves other sources alone", policies: append(shipped, sourceOnly), source: "ci", addr: "203.0.113.9", allowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(tt.policies)
			if err != nil {
				t.Fatal(err)
			}
			if got := engine.AuthorizeDelivery(org.DefaultOrgID, tt.source, tt.addr); got.Allowed != tt.allowed {
				t.Fatalf("allowed = %v, want %v (%+v)", got.Allowed, tt.allowed, got)
			}
		})
	}
}
