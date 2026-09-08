package archied

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// gatewayResolvedToken returns the Bearer [REDACTED] a Gateway client
// presents: the explicit [services.gateway].target_token key, then the
// GATEWAY_TOKEN secret/env. The server side (RunGateway) resolves the same
// value, so a distributed consumer and the gateway agree without sharing
// any other configuration.
func gatewayResolvedToken(settings config.ServiceConnection, secrets *secret.Registry) string {
	token := settings.TargetToken
	if token == "" {
		token = secrets.Getenv("GATEWAY_TOKEN")
	}
	return token
}

// composeChatContract dials the standalone archie-gateway, the sole owner of
// ChatContract, from the daemon's own configuration key. Token resolution is
// the daemon's (it owns the config keys and the secret registry); the dial
// itself and the fail-closed non-loopback rule belong to the transport, so
// they come from gatewayrpc.Dial.
func composeChatContract(settings config.ServiceConnection, secrets *secret.Registry, options ...grpc.DialOption) (gateway.ChatContract, func(), error) {
	if strings.TrimSpace(settings.Target) == "" {
		return nil, nil, fmt.Errorf("services.gateway.target is required")
	}
	// Assigned through a typed variable rather than returned directly: a
	// direct forward would hand a failed dial's nil *Client back as a
	// non-nil ChatContract interface.
	client, cleanup, err := gatewayrpc.Dial(settings.Target, gatewayResolvedToken(settings, secrets), options...)
	if err != nil {
		return nil, nil, err
	}
	return client, cleanup, nil
}
