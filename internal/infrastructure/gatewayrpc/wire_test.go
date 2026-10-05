package gatewayrpc

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestChatWireKeepsGatewayInputs pins the fields the Gateway decides on
// across the messaging-to-gateway hop.
func TestChatWireKeepsGatewayInputs(t *testing.T) {
	in := messaging.Inbound{Message: messaging.Message{SenderID: "42"}, Platform: "telegram"}
	got := inboundValue(inboundProto(in))
	if got.Message.SenderID != "42" || got.Platform != "telegram" {
		t.Fatalf("inbound round trip lost a field: %+v", got)
	}
}
