package controlplane_test

import (
	"encoding/json"
	"testing"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

func TestStoredChannelSettingsCanBeEdited(t *testing.T) {
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
	client := workflowRPC(t, server, db).ControlPlane()
	stored, err := db.PutResource(ctx, storecontract.ResourceWrite{
		OrgID: storecontract.DefaultOrgID, Kind: controlplane.ChannelSettingsKind,
		Value: []byte(`{"email":{"listen_addr":""},"webhook":{},"webhook_addr":"","telegram":{"allowed_user_ids":[42],"token_ref":{"engine":"bws","key":"telegram"}},"workspace":"/workspace","max_steps":17,"rate_limit":{"window":"30s","max_requests":5}}`),
		Actor: "operator", Source: "test", RequestID: "old-settings",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Query(ctx, &pb.QueryRequest{Kind: controlplane.ChannelSettingsKind})
	if err != nil {
		t.Fatal(err)
	}
	var settings controlplanerpc.ChannelSettings
	if err := json.Unmarshal(got.Resource.ValueJson, &settings); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(got.Resource.ValueJson, &document); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"email", "webhook", "webhook_addr"} {
		if _, exists := document[retired]; exists {
			t.Fatalf("served retired %s field in editable settings", retired)
		}
	}
	if settings.Telegram.Token.Key != "telegram" || len(settings.Telegram.AllowedUserIDs) != 1 || settings.Telegram.AllowedUserIDs[0] != 42 || settings.Workspace != "/workspace" || settings.MaxSteps != 17 || settings.RateLimit.MaxRequests != 5 {
		t.Fatalf("lost supported settings: %+v", settings)
	}
	settings.Telegram.AllowedUserIDs = []int64{42, 43}
	value, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := client.Command(ctx, &pb.CommandRequest{Kind: controlplane.ChannelSettingsKind, Command: "replace", ValueJson: value, ExpectedVersion: got.Resource.Version, Actor: "operator", Source: "test", RequestId: "edit"})
	if err != nil {
		t.Fatalf("saving edited channel settings: %v", err)
	}
	if changed.Resource.Version != stored.Version+1 {
		t.Fatalf("saved version %d", changed.Resource.Version)
	}
}
