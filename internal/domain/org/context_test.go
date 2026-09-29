package org

import (
	"context"
	"testing"
)

// foreignContextKey stands in for another package's key: a value stored under
// it must never be read back as the org.
type foreignContextKey struct{}

func TestOrgFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want OrgID
	}{
		{name: "unset is the default org", ctx: context.Background(), want: DefaultOrgID},
		{name: "set org", ctx: WithOrg(context.Background(), "acme"), want: "acme"},
		{
			name: "inner org overrides an outer one",
			ctx:  WithOrg(WithOrg(context.Background(), "outer"), "inner"),
			want: "inner",
		},
		{
			name: "nil stored value is the default org",
			ctx:  context.WithValue(context.Background(), orgContextKey{}, nil),
			want: DefaultOrgID,
		},
		{
			name: "a value of a foreign type is the default org",
			ctx:  context.WithValue(context.Background(), orgContextKey{}, "acme"),
			want: DefaultOrgID,
		},
		{
			name: "a value under a foreign key is the default org",
			ctx:  context.WithValue(context.Background(), foreignContextKey{}, OrgID("acme")),
			want: DefaultOrgID,
		},
		{name: "empty org is the default org", ctx: WithOrg(context.Background(), ""), want: DefaultOrgID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OrgFromContext(tt.ctx); got != tt.want {
				t.Fatalf("OrgFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}
