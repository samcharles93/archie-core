package archied

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
)

func TestComposeChatContractLocal(t *testing.T) {
	for _, mode := range []string{"", "inproc"} {
		t.Run("mode="+mode, func(t *testing.T) {
			local := &gateway.LocalChatAdapter{}
			chat, cleanup, err := composeChatContract(config.ServiceConnection{Mode: mode}, local)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup == nil {
				t.Fatal("local composition returned no cleanup")
			}
			t.Cleanup(cleanup)
			if chat != local {
				t.Fatalf("local composition returned %T instead of the supplied adapter", chat)
			}
		})
	}
}

func TestComposeChatContractRejectsInvalidSettings(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings config.ServiceConnection
	}{
		{name: "unknown mode", settings: config.ServiceConnection{Mode: "automatic"}},
		{name: "default with target", settings: config.ServiceConnection{Target: "localhost:1234"}},
		{name: "inproc with target", settings: config.ServiceConnection{Mode: "inproc", Target: "localhost:1234"}},
		{name: "remote missing target", settings: config.ServiceConnection{Mode: "remote"}},
		{name: "remote blank target", settings: config.ServiceConnection{Mode: "remote", Target: " \t\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			chat, cleanup, err := composeChatContract(tt.settings, &gateway.LocalChatAdapter{})
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

func TestComposeChatContractRemoteAndCleanup(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
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

	local := compositionChat{snapshot: gateway.ChatSnapshot{ActiveModel: "local-model"}}
	chat, cleanup, err := composeChatContract(
		config.ServiceConnection{Mode: "remote", Target: "passthrough:///chat"}, local,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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

type compositionChat struct {
	gateway.ChatContract
	snapshot gateway.ChatSnapshot
}

func (c compositionChat) Snapshot(context.Context) (gateway.ChatSnapshot, error) {
	return c.snapshot, nil
}
