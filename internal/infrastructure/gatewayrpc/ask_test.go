package gatewayrpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// askingChat is a turn that asks for approval and reports what it got.
type askingChat struct{ messaging.ChatContract }

func (askingChat) Stream(ctx context.Context, _ messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	out := make(chan messaging.ChatEvent, 1)
	go func() {
		defer close(out)
		approver := messaging.ApprovalFromContext(ctx)
		if approver == nil {
			out <- messaging.ChatEvent{Kind: "done", Text: "not configured"}
			return
		}
		decision, err := approver.RequestApproval(ctx, "shell", "rm -rf /tmp/x")
		switch {
		case err != nil:
			out <- messaging.ChatEvent{Kind: "done", Text: "error"}
		case decision == messaging.ApprovalDenied:
			out <- messaging.ChatEvent{Kind: "done", Text: "denied"}
		default:
			out <- messaging.ChatEvent{Kind: "done", Text: "approved"}
		}
	}()
	return out, nil
}

type fixedApprover messaging.ApprovalDecision

func (f fixedApprover) RequestApproval(context.Context, string, string) (messaging.ApprovalDecision, error) {
	return messaging.ApprovalDecision(f), nil
}

// TestStreamCarriesApproval: an approval-gated turn in the gateway reaches the
// caller's own approver, and a caller with none is never asked.
func TestStreamCarriesApproval(t *testing.T) {
	listener := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	RegisterServer(srv, askingChat{}, nil, Catalog{})
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := NewClient(conn)

	tests := []struct {
		name     string
		approver messaging.ApprovalRequester
		want     string
	}{
		{name: "approved by the caller", approver: fixedApprover(messaging.ApprovalApproved), want: "approved"},
		{name: "denied by the caller", approver: fixedApprover(messaging.ApprovalDenied), want: "denied"},
		{name: "a caller with no approver is not asked", want: "not configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if tt.approver != nil {
				ctx = messaging.WithApprovalRequester(ctx, tt.approver)
			}
			events, err := client.Stream(ctx, messaging.Inbound{})
			if err != nil {
				t.Fatal(err)
			}
			var got string
			for event := range events {
				got = event.Text
			}
			if got != tt.want {
				t.Fatalf("turn saw %q, want %q", got, tt.want)
			}
		})
	}
}
