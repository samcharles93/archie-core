package archied

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/sdnotify"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

func TestGatewayServerOptsLoopbackIsInsecure(t *testing.T) {
	opts, loopback, err := gatewayServerOpts("127.0.0.1:8585", "")
	if err != nil {
		t.Fatalf("gatewayServerOpts(loopback, no token) error: %v", err)
	}
	if !loopback {
		t.Fatal("loopback listener should report loopback")
	}
	// Only the inbound size limit; a loopback listener installs no auth
	// interceptors (the count is the only handle on opaque ServerOptions).
	if len(opts) != 1 {
		t.Fatalf("loopback listener should install only the inbound size limit, got %d options", len(opts))
	}
}

func TestGatewayServerOptsNonLoopbackRequiresToken(t *testing.T) {
	if _, _, err := gatewayServerOpts("0.0.0.0:8585", ""); err == nil {
		t.Fatal("non-loopback listener without a token should fail closed")
	}
	opts, loopback, err := gatewayServerOpts("0.0.0.0:8585", "secret")
	if err != nil {
		t.Fatalf("non-loopback listener with a token error: %v", err)
	}
	if loopback {
		t.Fatal("non-loopback listener should not report loopback")
	}
	if len(opts) != 3 {
		t.Fatalf("non-loopback listener should install three server options (inbound size limit plus unary and stream auth interceptors), got %d", len(opts))
	}
}

func TestGatewayServerOptsMalformedListen(t *testing.T) {
	if _, _, err := gatewayServerOpts("not-an-address", ""); err == nil {
		t.Fatal("malformed listen address should error")
	}
}

// stubGatewayContract satisfies messaging.ChatContract with zero values: the
// serve test exercises the listener, the READY announcement and one read RPC,
// not the chat runtime.
type stubGatewayContract struct{}

func (stubGatewayContract) Snapshot(context.Context) (messaging.ChatSnapshot, error) {
	return messaging.ChatSnapshot{}, nil
}

func (stubGatewayContract) GetSession(context.Context, string) (messaging.SessionContext, bool, error) {
	return messaging.SessionContext{}, false, nil
}

func (stubGatewayContract) RecentMessages(context.Context, string, int) ([]messaging.Message, error) {
	return nil, nil
}

func (stubGatewayContract) RecentTurns(context.Context, string, int) ([]messaging.TurnRecord, error) {
	return nil, nil
}

func (stubGatewayContract) Route(context.Context, messaging.Inbound) (messaging.ChatReply, error) {
	return messaging.ChatReply{}, nil
}

func (stubGatewayContract) Stream(context.Context, messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	return nil, nil
}

func (stubGatewayContract) Cancel(context.Context, string) (messaging.ChatCancellation, error) {
	return messaging.ChatCancellation{}, nil
}

func (stubGatewayContract) SetPersona(context.Context, string, string) (bool, error) {
	return false, nil
}

func (stubGatewayContract) ApplyTaskAction(context.Context, string, int64, taskstate.Action, taskactions.ReviewResponse) (messaging.TaskActionResult, error) {
	return messaging.TaskActionResult{}, nil
}

func (stubGatewayContract) ApplyOperatorTaskAction(context.Context, taskactions.Actor, int64, taskstate.Action, taskactions.ReviewResponse) (messaging.TaskActionResult, error) {
	return messaging.TaskActionResult{}, nil
}

// TestGatewayServingAnnouncesReady: archie-gateway run as a Type=notify unit
// must send READY=1 at the moment it declares itself serving, or systemd kills
// a healthy process once TimeoutStartSec expires (archie-core-1174). The
// announcement is judged against the same evidence archied's run loop uses:
// the contract answers over the bound listener, so READY asserts a serving
// process rather than a bound socket.
func TestGatewayServingAnnouncesReady(t *testing.T) {
	listener := newNotifyListener(t)
	b := &boot{log: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	serveCh := make(chan error, 1)
	go func() { serveCh <- b.serveGatewayListener(ctx, ln, true, stubGatewayContract{}, nil) }()

	if !listener.waitForState(t, 5*time.Second, sdnotify.ReadyState) {
		t.Fatal("no READY=1 datagram within 5s of the Gateway serving")
	}

	conn, err := grpc.NewClient("passthrough:///"+ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := pb.NewChatServiceClient(conn).Snapshot(t.Context(), &pb.SnapshotRequest{}); err != nil {
		t.Fatalf("Snapshot over gRPC: %v", err)
	}

	cancel()
	if err := <-serveCh; err != nil {
		t.Fatalf("serveGatewayListener returned: %v", err)
	}
}

// recordingGatewayContract captures the inbound a Stream call reached the
// Gateway with, so a test can prove the request's payload survived the wire.
type recordingGatewayContract struct {
	stubGatewayContract
	mu   sync.Mutex
	last messaging.Inbound
}

func (c *recordingGatewayContract) Stream(_ context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	c.mu.Lock()
	c.last = in
	c.mu.Unlock()
	events := make(chan messaging.ChatEvent)
	close(events)
	return events, nil
}

// TestGatewayAcceptsAnInboundAttachmentOverTheDefaultGRPCLimit: the inbound
// request now carries a channel attachment's bytes, and gRPC's default receive
// limit is 4 MB -- well under the messaging contract's ceiling. A maximal
// attachment must reach the turn, not fail the RPC with ResourceExhausted.
func TestGatewayAcceptsAnInboundAttachmentOverTheDefaultGRPCLimit(t *testing.T) {
	rec := &recordingGatewayContract{}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	opts, _, err := gatewayServerOpts(ln.Addr().String(), "")
	if err != nil {
		t.Fatalf("gatewayServerOpts: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	serveCh := make(chan error, 1)
	go func() { serveCh <- serveGateway(ctx, ln, rec, nil, opts) }()
	defer func() {
		cancel()
		if err := <-serveCh; err != nil {
			t.Errorf("serveGateway: %v", err)
		}
	}()

	conn, err := grpc.NewClient("passthrough:///"+ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	raw := make([]byte, 5<<20) // larger than gRPC's 4 MB default receive limit
	stream, err := pb.NewChatServiceClient(conn).Stream(t.Context(), &pb.StreamRequest{
		Message: &pb.Message{Media: []*pb.Media{{Type: "image", MimeType: "image/jpeg", Data: raw}}},
	})
	if err != nil {
		t.Fatalf("Stream with a %d-byte attachment: %v", len(raw), err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv after an empty stream: %v, want EOF", err)
	}

	rec.mu.Lock()
	got := rec.last
	rec.mu.Unlock()
	if len(got.Media) != 1 {
		t.Fatalf("turn received %d attachments, want 1", len(got.Media))
	}
	if len(got.Media[0].Data) != len(raw) {
		t.Fatalf("turn received %d attachment bytes, want %d", len(got.Media[0].Data), len(raw))
	}
}
