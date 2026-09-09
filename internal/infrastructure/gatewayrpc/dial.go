package gatewayrpc

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial returns a chat contract client for target, the standalone
// archie-gateway process, applying the transport security boundary: a
// loopback target dials insecure with no credential, while a non-loopback
// target requires a Bearer [REDACTED] and fails closed without one. The returned
// cleanup closes the connection.
//
// The rule lives here rather than in a composition root so the server side
// (which decides whether to install the token interceptors) and every client
// that dials it share one implementation. It mirrors staterpc.Dial.
func Dial(target, token string, options ...grpc.DialOption) (*Client, func(), error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil, fmt.Errorf("gateway target is required")
	}
	loopback, err := TargetIsLoopback(target)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway target must be host:port: %w", err)
	}
	if !loopback && token == "" {
		return nil, nil, fmt.Errorf(
			"gateway target %q is non-loopback; non-loopback exposure requires a Bearer [REDACTED] ([services.gateway].target_token / GATEWAY_TOKEN) or TLS",
			target,
		)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		opts = append(
			opts,
			grpc.WithUnaryInterceptor(UnaryClientTokenInterceptor(token)),
			grpc.WithStreamInterceptor(StreamClientTokenInterceptor(token)),
		)
	}
	conn, err := grpc.NewClient(target, append(opts, options...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("create gateway client: %w", err)
	}
	return NewClient(conn), func() { _ = conn.Close() }, nil
}
