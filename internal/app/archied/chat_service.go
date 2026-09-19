package archied

import (
	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// composeChatContract dials the standalone archie-gateway, the sole owner of
// ChatContract, from the daemon's own configuration key. Token resolution is
// the daemon's (it owns the config keys and the secret registry); the dial
// itself and the fail-closed non-loopback rule belong to the transport, so
// they come from gatewayrpc.Dial.
func composeChatContract(services config.Services, secrets *secret.Registry, options ...grpc.DialOption) (gateway.ChatContract, func(), error) {
	target, err := services.RequireTarget(config.ServiceNameGateway)
	if err != nil {
		return nil, nil, err
	}
	// Assigned through a typed variable rather than returned directly: a
	// direct forward would hand a failed dial's nil *Client back as a
	// non-nil ChatContract interface.
	client, cleanup, err := gatewayrpc.Dial(target, services.ResolvedToken(config.ServiceNameGateway, secrets.Getenv), options...)
	if err != nil {
		return nil, nil, err
	}
	return client, cleanup, nil
}
