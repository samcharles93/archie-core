package postgres_test

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// An org's sources, event types, mappings and bindings are invisible to a
// request scoped to another org, which can neither change nor delete them.
// An unscoped internal service still sees them.
func TestEDARecordsStayInTheirOrg(t *testing.T) {
	scoped := func(id org.OrgID) context.Context {
		return org.WithScope(org.WithOrg(t.Context(), id), id)
	}
	acme := scoped("acme")
	eda := postgres.NewEDA(pgstore.Pool(t), nil)
	if err := eda.InsertSource(acme, source.Source{Path: "acme-hook", Signing: source.SigningUnsigned}); err != nil {
		t.Fatal(err)
	}
	typeID, err := eda.InsertEventType(acme, eventtype.EventType{Source: "acme-hook", Name: "push"})
	if err != nil {
		t.Fatal(err)
	}
	mappingID, err := eda.InsertMapping(acme, mapping.Mapping{Name: "push", EventTypeID: typeID})
	if err != nil {
		t.Fatal(err)
	}
	bindingID, err := eda.InsertBinding(acme, binding.Binding{Name: "deploy", Matcher: binding.Matcher{Source: "acme-hook"}, MappingID: mappingID, Workflow: "deploy"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		ctx  func() context.Context //nolint:containedctx // table input, not stored state
		sees bool
	}{
		{"the owning org", func() context.Context { return acme }, true},
		{"another org", func() context.Context { return scoped("other") }, false},
		{"an internal service", t.Context, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx()
			sources, err := eda.ListSources(ctx)
			if err != nil {
				t.Fatal(err)
			}
			src, err := eda.GetSource(ctx, "acme-hook")
			if err != nil {
				t.Fatal(err)
			}
			types, err := eda.ListEventTypes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mappings, err := eda.ListMappings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			m, err := eda.GetMapping(ctx, mappingID)
			if err != nil {
				t.Fatal(err)
			}
			bindings, err := eda.ListBindings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			b, err := eda.GetBinding(ctx, bindingID)
			if err != nil {
				t.Fatal(err)
			}
			seen := []bool{len(sources) == 1, src != nil, len(types) == 1, len(mappings) == 1, m != nil, len(bindings) == 1, b != nil}
			for i, s := range seen {
				if s != tt.sees {
					t.Fatalf("read %d visible = %v, want %v", i, s, tt.sees)
				}
			}
			if tt.sees {
				return
			}
			if err := eda.ApproveBinding(ctx, bindingID); err == nil {
				t.Fatal("approved another org's binding")
			}
			if err := eda.SetSourceSecret(ctx, "acme-hook", "stolen"); err == nil {
				t.Fatal("rotated another org's source secret")
			}
			if err := eda.DeleteBinding(ctx, bindingID); err == nil {
				t.Fatal("deleted another org's binding")
			}
			if err := eda.DeleteMapping(ctx, mappingID); err == nil {
				t.Fatal("deleted another org's mapping")
			}
			if err := eda.DeleteEventType(ctx, typeID); err == nil {
				t.Fatal("deleted another org's event type")
			}
		})
	}
}
