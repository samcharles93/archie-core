package memory_test

import (
	"testing"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/infrastructure/memory"
)

// A fact written in one org's turn reaches that org's turns only, in every
// scope a turn reads, including global.
func TestMemoryStaysInItsOrg(t *testing.T) {
	engine := memory.NewBuiltinEngine(t.TempDir(), 1<<20)
	subject := func(id org.OrgID) domainmemory.Subject {
		return domainmemory.Subject{AgentID: "bot", UserID: "sam", Org: id}
	}
	for _, scope := range subject("acme").Scopes() {
		if _, err := engine.Create(t.Context(), domainmemory.NewRecord{Scope: scope, Kind: "note", Content: "acme " + string(scope.Kind), Author: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name string
		org  org.OrgID
		want int
	}{
		{"the owning org", "acme", 4},
		{"another org", "other", 0},
		{"the system org", org.DefaultOrgID, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.Query(t.Context(), domainmemory.Query{Scopes: subject(tt.org).Scopes()})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Fatalf("read %d records, want %d", len(got), tt.want)
			}
		})
	}
}
