package org

import (
	"context"
	"testing"
)

// foreignContextKey stands in for another package's key: a value stored under
// it must never be read back as the org.
type foreignContextKey struct{}

func TestOrgFromContext(t *testing.T) {
	// build derives each case's context from a fresh background one. The table
	// holds no context.Context field on purpose: a stored context is what
	// containedctx rejects, and a builder keeps every case readable without one.
	tests := []struct {
		name  string
		build func(context.Context) context.Context
		want  OrgID
	}{
		{name: "unset is the default org", build: func(ctx context.Context) context.Context { return ctx }, want: DefaultOrgID},
		{name: "set org", build: func(ctx context.Context) context.Context { return WithOrg(ctx, "acme") }, want: "acme"},
		{
			name:  "inner org overrides an outer one",
			build: func(ctx context.Context) context.Context { return WithOrg(WithOrg(ctx, "outer"), "inner") },
			want:  "inner",
		},
		{
			name:  "nil stored value is the default org",
			build: func(ctx context.Context) context.Context { return context.WithValue(ctx, orgContextKey{}, nil) },
			want:  DefaultOrgID,
		},
		{
			name:  "a value of a foreign type is the default org",
			build: func(ctx context.Context) context.Context { return context.WithValue(ctx, orgContextKey{}, "acme") },
			want:  DefaultOrgID,
		},
		{
			name: "a value under a foreign key is the default org",
			build: func(ctx context.Context) context.Context {
				return context.WithValue(ctx, foreignContextKey{}, OrgID("acme"))
			},
			want: DefaultOrgID,
		},
		{name: "empty org is the default org", build: func(ctx context.Context) context.Context { return WithOrg(ctx, "") }, want: DefaultOrgID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OrgFromContext(tt.build(context.Background())); got != tt.want {
				t.Fatalf("OrgFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}
