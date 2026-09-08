package archied

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

func TestComposeChatContractRejectsInvalidSettings(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings config.ServiceConnection
	}{
		{name: "missing target", settings: config.ServiceConnection{}},
		{name: "blank target", settings: config.ServiceConnection{Target: " \t\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			chat, cleanup, err := composeChatContract(tt.settings, &secret.Registry{})
			if cleanup != nil {
				t.Cleanup(cleanup)
			}
			if err == nil {
				t.Fatal("invalid settings were accepted")
			}
			if chat != nil || cleanup != nil {
				t.Fatal("invalid settings published an adapter or cleanup")
			}
		})
	}
}

func TestComposeChatContractNonLoopbackFailsClosed(t *testing.T) {
	_, _, err := composeChatContract(
		config.ServiceConnection{Target: "gateway.example.com:8585"},
		&secret.Registry{},
	)
	if err == nil {
		t.Fatal("non-loopback target without a token should fail closed")
	}
}

func TestComposeChatContractRemoteAndCleanup(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	gatewayrpc.RegisterServer(server, compositionChat{snapshot: gateway.ChatSnapshot{ActiveModel: "remote-model"}})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		if err := <-serveDone; err != nil {
			t.Errorf("serve chat: %v", err)
		}
	})

	chat, cleanup, err := composeChatContract(
		config.ServiceConnection{Target: listener.Addr().String()},
		&secret.Registry{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil {
		t.Fatal("remote composition returned no cleanup")
	}
	t.Cleanup(cleanup)
	if _, ok := chat.(*gatewayrpc.Client); !ok {
		t.Fatalf("remote composition returned %T", chat)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := chat.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveModel != "remote-model" {
		t.Fatalf("Snapshot used wrong adapter: ActiveModel = %q", snapshot.ActiveModel)
	}
	cleanup()
	if _, err := chat.Snapshot(ctx); status.Code(err) != codes.Canceled {
		t.Fatalf("Snapshot after cleanup: code = %v, want Canceled; error = %v", status.Code(err), err)
	}
}

func TestGatewayResolvedToken(t *testing.T) {
	if got := gatewayResolvedToken(config.ServiceConnection{TargetToken: "file-token"}, &secret.Registry{}); got != "file-token" {
		t.Fatalf("resolved token = %q, want file-token", got)
	}
	t.Setenv("GATEWAY_TOKEN", "environment-token")
	if got := gatewayResolvedToken(config.ServiceConnection{}, &secret.Registry{}); got != "environment-token" {
		t.Fatalf("resolved token = %q, want environment-token", got)
	}
	if got := gatewayResolvedToken(
		config.ServiceConnection{TargetToken: "file-token"},
		&secret.Registry{},
	); got != "file-token" {
		t.Fatalf("explicit target_token should win over the environment, got %q", got)
	}
}

type compositionChat struct {
	gateway.ChatContract
	snapshot gateway.ChatSnapshot
}

func (c compositionChat) Snapshot(context.Context) (gateway.ChatSnapshot, error) {
	return c.snapshot, nil
}
