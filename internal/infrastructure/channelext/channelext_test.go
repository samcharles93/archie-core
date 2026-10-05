package channelext

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gatewayv1 "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type recordingChat struct {
	messaging.ChatContract
	operatorCalls int
}

func (c *recordingChat) ApplyOperatorTaskAction(context.Context, taskactions.Actor, int64, taskstate.Action, taskactions.ActionPayload) (messaging.TaskActionResult, error) {
	c.operatorCalls++
	return messaging.TaskActionResult{}, nil
}

// A channel extension reaches the gateway through the chat surface the host
// serves it; operator-level task actions must not be part of that surface.
func TestChannelExtensionCannotActAsOperator(t *testing.T) {
	chat := &recordingChat{}
	server := grpc.NewServer()
	gatewayrpc.RegisterServer(server, restricted{chat}, nil, gatewayrpc.Catalog{})
	lc := net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	_, err = gatewayv1.NewChatServiceClient(conn).ApplyOperatorTaskAction(t.Context(), &gatewayv1.ApplyOperatorTaskActionRequest{TaskId: 1, Action: "retry"})
	if err == nil {
		t.Fatal("operator task action succeeded through a channel extension's chat surface")
	}
	if chat.operatorCalls != 0 {
		t.Fatalf("the gateway was asked to apply an operator action %d times", chat.operatorCalls)
	}
}
