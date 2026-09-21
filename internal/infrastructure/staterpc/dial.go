package staterpc

import (
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
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
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Client keepalive. grpc-go's default client keepalive time is
		// infinity (internal/transport.defaultClientKeepaliveTime), so a
		// connection that half-opens under an idle client -- peer gone, socket
		// still ESTABLISHED -- produces no error here. That is load-bearing for
		// this dial because the control-plane Watch stream is silent by design
		// while the watched resource's version is unchanged
		// (internal/app/controlplane's Watch): with neither peer writing,
		// neither notices, and the caller's watch stays frozen for the life of
		// the process with no error and no log.
		//
		// Time 10s is the soonest a dead peer can be detected: gRPC clamps a
		// client ping interval up to internal.KeepaliveMinPingTime (10s), and
		// 10s of connection-level silence costs one HTTP/2 PING, which is far
		// below the traffic this link already carries (the store service polls
		// its store every 250ms per watch). Timeout 5s bounds detection at
		// ~15s: a PING the peer does not ACK within 5s tears the transport
		// down, turning a half-open connection into a stream error the caller
		// can act on.
		grpc.WithKeepaliveParams(clientKeepaliveParams()),
	}
	if token != "" {
		// Both call shapes need the credential: the server's interceptors
		// guard unary RPCs and the streaming capture reads separately, so a
		// unary-only interceptor leaves the streams unauthenticated.
		opts = append(
			opts,
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

// clientKeepaliveParams are the client keepalive settings Dial installs; see
// Dial's option comment for why each value is what it is. PermitWithoutStream
// is deliberately left false (the library default): the pings this dial needs
// are covered by an active stream, and pinging a connection with no streams is
// what a peer's keepalive enforcement policy records as a ping strike --
// against the State Store's default policy a streamless ping draws GOAWAY
// too_many_pings and would churn connections that are healthy today.
func clientKeepaliveParams() keepalive.ClientParameters {
	return keepalive.ClientParameters{
		Time:    10 * time.Second,
		Timeout: 5 * time.Second,
	}
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
