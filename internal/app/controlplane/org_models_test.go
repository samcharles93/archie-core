package controlplane_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// An org reaches only the models the instance admin allows it and the
// providers it was allowed to add; an org with no policy reaches nothing.
func TestOrgModelPolicy(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	if err := db.BootstrapIdentities(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.UpgradeDefaultOrg(ctx); err != nil {
		t.Fatal(err)
	}
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.NewServer(db, steps)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.Create(ctx, identity.Identity{ID: "owner", Kind: identity.KindUser, DisplayName: "Owner", Lifecycle: identity.LifecycleActive}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "owner", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []org.OrgID{"acme", "newco"} {
		if _, err := db.CreateOrg(ctx, org.Org{ID: id, Name: string(id)}, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	sys := org.WithOrg(ctx, org.DefaultOrgID)
	acme := org.WithOrg(ctx, "acme")
	put := func(ctx context.Context, kind, value string) error {
		_, err := server.Command(ctx, &pb.CommandRequest{Kind: kind, Command: "replace", ValueJson: []byte(value), Actor: "test", Source: "test", RequestId: kind + value})
		return err
	}
	for _, w := range [][2]string{
		{controlplane.ProviderSettingsKind, `{"openai":{"class":"openai","api_key_ref":{}},"deepseek":{"class":"deepseek","api_key_ref":{}}}`},
		{controlplane.ModelAliasesKind, `{"default":"openai/gpt-6-luna","cheap":"deepseek/chat"}`},
		{controlplane.OrgModelPolicyKind, `{"acme":{"allowed":["openai/*"],"own_providers":true}}`},
	} {
		if err := put(sys, w[0], w[1]); err != nil {
			t.Fatalf("seed %s: %v", w[0], err)
		}
	}

	for _, tt := range []struct {
		name, kind, value string
		ok                bool
	}{
		{"an alias to a model the org may not use", controlplane.ModelAliasesKind, `{"fast":"deepseek/chat"}`, false},
		{"an org provider named like an instance one", controlplane.ProviderSettingsKind, `{"openai":{"class":"openai","api_key_ref":{}}}`, false},
		{"an org provider pointing at a secret", controlplane.ProviderSettingsKind, `{"mine":{"class":"openai","api_key_ref":{"engine":"env","key":"X"}}}`, false},
		{"an org writing the instance policy", controlplane.OrgModelPolicyKind, `{"acme":{"allowed":["deepseek/*"]}}`, false},
		{"an org provider of its own", controlplane.ProviderSettingsKind, `{"mine":{"class":"openai","api_key_ref":{}}}`, true},
		{"aliases to allowed and own models", controlplane.ModelAliasesKind, `{"fast":"openai/gpt-6-mini","own":"mine/m1"}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := put(acme, tt.kind, tt.value); (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok %v", err, tt.ok)
			}
		})
	}

	for _, tt := range []struct {
		org           string
		aliases, prov []string
	}{
		{"acme", []string{"default", "fast", "own"}, []string{"mine"}},
		{"newco", nil, nil},
		{string(org.DefaultOrgID), []string{"cheap", "default"}, nil},
	} {
		t.Run("resolve "+tt.org, func(t *testing.T) {
			models, err := server.ModelsFor(ctx, tt.org)
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Sorted(maps.Keys(models.Aliases)); strings.Join(got, ",") != strings.Join(tt.aliases, ",") {
				t.Fatalf("aliases = %v, want %v", got, tt.aliases)
			}
			if got := slices.Sorted(maps.Keys(models.Providers)); strings.Join(got, ",") != strings.Join(tt.prov, ",") {
				t.Fatalf("providers = %v, want %v", got, tt.prov)
			}
		})
	}
}
