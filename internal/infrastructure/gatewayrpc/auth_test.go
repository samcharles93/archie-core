package gatewayrpc

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// authChat is a minimal ChatContract for the auth boundary: only Snapshot
// and Stream are exercised, the rest fall through to the nil embedded
// contract and are never called.
type authChat struct {
	gateway.ChatContract
	snapshot gateway.ChatSnapshot
}

func (f *authChat) Snapshot(context.Context) (gateway.ChatSnapshot, error) {
	return f.snapshot, nil
}

func (f *authChat) Stream(context.Context, gateway.Message) (<-chan gateway.ChatEvent, error) {
	ch := make(chan gateway.ChatEvent)
	close(ch)
	return ch, nil
}

// serveAuth serves the chat contract on a loopback listener with the token
// interceptors installed when token is non-empty, mirroring the standalone
// gateway composition. It returns the dial target.
func serveAuth(t *testing.T, token string) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var opts []grpc.ServerOption
	if token != "" {
		opts = []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(UnaryServerInterceptor(token)),
			grpc.ChainStreamInterceptor(StreamServerInterceptor(token)),
		}
	}
	server := grpc.NewServer(opts...)
	RegisterServer(server, &authChat{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func TestGatewayTokenRoundTrip(t *testing.T) {
	target := serveAuth(t, "correct-token")
	client, cleanup, err := Dial(target, "correct-token")
	if err != nil {
		t.Fatalf("Dial with the correct token: %v", err)
	}
	defer cleanup()
	if _, err := client.Snapshot(t.Context()); err != nil {
		t.Fatalf("Snapshot with the correct token: %v", err)
	}
	events, err := client.Stream(t.Context(), gateway.Message{})
	if err != nil {
		t.Fatalf("Stream with the correct token: %v", err)
	}
	for range events {
	}
}

func TestGatewayTokenMissingIsRejected(t *testing.T) {
	target := serveAuth(t, "correct-token")
	// A loopback target dials without a credential, so the dial itself
	// succeeds; the server rejects the unauthenticated call.
	client, cleanup, err := Dial(target, "")
	if err != nil {
		t.Fatalf("Dial to a loopback target without a token: %v", err)
	}
	defer cleanup()
	if _, err := client.Snapshot(t.Context()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Snapshot without a token: code = %v, want Unauthenticated (err = %v)", status.Code(err), err)
	}
}

func TestGatewayTokenWrongIsRejected(t *testing.T) {
	target := serveAuth(t, "correct-token")
	client, cleanup, err := Dial(target, "wrong-token")
	if err != nil {
		t.Fatalf("Dial with a token: %v", err)
	}
	defer cleanup()
	if _, err := client.Snapshot(t.Context()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Snapshot with the wrong token: code = %v, want Unauthenticated (err = %v)", status.Code(err), err)
	}
}

func TestGatewayStreamWithoutTokenIsRejected(t *testing.T) {
	target := serveAuth(t, "correct-token")
	client, cleanup, err := Dial(target, "")
	if err != nil {
		t.Fatalf("Dial to a loopback target without a token: %v", err)
	}
	defer cleanup()
	events, err := client.Stream(t.Context(), gateway.Message{})
	if err != nil {
		t.Fatalf("Stream without a token: %v", err)
	}
	event, ok := <-events
	if !ok {
		t.Fatal("Stream without a token closed with no error event")
	}
	if event.Kind != "error" {
		t.Fatalf("Stream without a token: event kind = %q, want error", event.Kind)
	}
}
