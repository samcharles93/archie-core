package staterpc

import (
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial returns a State Store contract client for target, applying the
// transport security boundary (docs/prds/state-store-contract.md §9): a
// loopback target dials insecure with no credential, while a non-loopback
// target requires a bearer token and fails closed without one. The returned
// cleanup closes the connection; Client.Close is a no-op because the store
// service owns its own DB lifecycle (§11).
//
// The rule lives here rather than in a composition root so the server side
// (which decides whether to install the token interceptors) and every client
// that dials it share one implementation.
func Dial(target, token string, options ...grpc.DialOption) (*Client, func(), error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil, fmt.Errorf("state store target is required")
	}
	loopback, err := TargetIsLoopback(target)
	if err != nil {
		return nil, nil, fmt.Errorf("state store target must be host:port: %w", err)
	}
	if !loopback && token == "" {
		return nil, nil, fmt.Errorf(
			"state store target %q is non-loopback; non-loopback exposure requires a bearer token ([services.state].target_token / STATE_STORE_TOKEN) or TLS, mirroring the gateway's --listen confinement (docs/prds/state-store-contract.md §9)",
			target,
		)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		// Both call shapes need the credential: the server's interceptors
		// guard unary RPCs and the streaming capture reads separately, so a
		// unary-only interceptor leaves the streams unauthenticated.
		opts = append(opts,
			grpc.WithUnaryInterceptor(UnaryClientTokenInterceptor(token)),
			grpc.WithStreamInterceptor(StreamClientTokenInterceptor(token)),
		)
	}
	conn, err := grpc.NewClient(target, append(opts, options...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("create state store client: %w", err)
	}
	return NewClient(conn), func() { _ = conn.Close() }, nil
}

// TargetIsLoopback reports whether addr's host is a loopback address. Both
// literal loopback IPs (127.0.0.1, ::1) and the "localhost" hostname count;
// everything else (including the wildcard 0.0.0.0 and DNS names) is treated
// as network-reachable and therefore non-loopback. It applies to a listen
// address and a dial target alike -- the same host decides both.
func TargetIsLoopback(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("address must be host:port: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, nil
	}
	return ip.IsLoopback(), nil
}
