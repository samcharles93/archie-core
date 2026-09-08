package archied

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
)

// composeChatContract dials the standalone archie-gateway, the sole owner of
// ChatContract, from the daemon's own configuration key.
func composeChatContract(settings config.ServiceConnection, options ...grpc.DialOption) (gateway.ChatContract, func(), error) {
	if strings.TrimSpace(settings.Target) == "" {
		return nil, nil, fmt.Errorf("services.gateway.target is required")
	}
	// Assigned through a typed variable rather than returned directly: a
	// direct forward would hand a failed dial's nil *Client back as a
	// non-nil ChatContract interface.
	client, cleanup, err := gatewayrpc.Dial(settings.Target, options...)
	if err != nil {
		return nil, nil, err
	}
	return client, cleanup, nil
}
