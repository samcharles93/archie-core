package controlplane_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/rpcidentity"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

func TestWebChatSettingsActor(t *testing.T) {
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
	cp, err := controlplane.NewServer(db, steps)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cp.ImportConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	state := workflowRPC(t, cp, db)
	router := gateway.NewRouter(state, nil, "web")
	router.Settings = messaging.NewSettingsCommand(controlplanerpc.NewSettingsClient(state.ControlPlane())).WithIdentities(state)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callers := rpcidentity.Callers{Principals: state}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(gatewayrpc.UnaryServerInterceptor("gateway-secret"), callers.Unary()), grpc.ChainStreamInterceptor(gatewayrpc.StreamServerInterceptor("gateway-secret"), callers.Stream()))
	gatewayrpc.RegisterServer(srv, &gateway.LocalChatAdapter{Router: router}, nil, gatewayrpc.Catalog{})
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	person, err := db.PrincipalFor(ctx, identity.OperatorID())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, token, sender   string
		person, write, stream bool
		code                  codes.Code
	}{
		{name: "streamed signed-in person", stream: true, token: "gateway-secret", person: true, write: true},
		{name: "streamed wrong service credential", stream: true, token: "wrong", person: true, code: codes.Unauthenticated},
		{name: "signed-in person", token: "gateway-secret", person: true, write: true},
		{name: "message cannot spoof actor", token: "gateway-secret", sender: string(identity.SystemID), person: true, write: true},
		{name: "message alone cannot name a person", token: "gateway-secret", sender: string(identity.OperatorID())},
		{name: "wrong service credential cannot forward a principal", token: "wrong", person: true, code: codes.Unauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(gatewayrpc.UnaryClientTokenInterceptor(tt.token)), grpc.WithStreamInterceptor(gatewayrpc.StreamClientTokenInterceptor(tt.token)))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			requestCtx := ctx
			if tt.person {
				requestCtx = access.WithPrincipal(ctx, person)
			}
			before, err := db.Resource(ctx, string(org.DefaultOrgID), controlplane.ReviewSettingsKind)
			if err != nil {
				t.Fatal(err)
			}
			in := messaging.Inbound{Platform: "web", Message: messaging.Message{SenderID: tt.sender, Text: "/settings set review-settings precision_gate=true"}}
			client := gatewayrpc.NewClient(conn)
			reply, err := settingsReply(t, client, requestCtx, in, tt.stream, tt.code)
			if status.Code(err) != tt.code {
				t.Fatalf("route error = %v, want %s", err, tt.code)
			}
			after, err := db.Resource(ctx, string(org.DefaultOrgID), controlplane.ReviewSettingsKind)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.write {
				if after.Version != before.Version {
					t.Fatal("unauthenticated command changed settings")
				}
				return
			}
			history, err := db.ResourceHistory(ctx, string(org.DefaultOrgID), controlplane.ReviewSettingsKind, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 {
				t.Fatal("settings audit missing")
			}
			if !strings.Contains(reply.Text, "Updated review-settings") || after.Version != before.Version+1 || history[0].Actor != string(person.IdentityID) {
				t.Fatalf("reply %q, version %d -> %d, actor %q", reply.Text, before.Version, after.Version, history[0].Actor)
			}
		})
	}
}

func settingsReply(t *testing.T, client *gatewayrpc.Client, ctx context.Context, in messaging.Inbound, stream bool, want codes.Code) (messaging.ChatReply, error) {
	t.Helper()
	if !stream {
		return client.Route(ctx, in)
	}
	events, err := client.Stream(ctx, in)
	if err != nil {
		return messaging.ChatReply{}, err
	}
	var reply messaging.ChatReply
	for event := range events {
		if event.Kind == "error" {
			if want == codes.OK || !strings.Contains(event.Text, "code = "+want.String()) {
				t.Fatalf("stream: %s", event.Text)
			}
			return messaging.ChatReply{}, status.Error(want, event.Text)
		}
		if event.Kind == "done" {
			reply.Text = event.Text
		}
	}
	return reply, nil
}
