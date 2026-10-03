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

// Dial returns a State Store client for target. A loopback target dials
// insecure; any other target requires a token. The cleanup closes the
// connection.
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
		// Keepalive detects a dead peer on an otherwise silent watch stream.
		grpc.WithKeepaliveParams(ClientKeepaliveParams()),
		// GetInstalledPackage returns the whole layer, which for an extension is
		// a plugin binary well past gRPC's default 4 MiB receive limit.
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxPackageResponseBytes)),
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

// maxPackageResponseBytes is the package layer cap plus headroom for the rest
// of the response.
const maxPackageResponseBytes = 80 << 20

// ClientKeepaliveParams are Dial's keepalive settings: ping after 10s idle,
// 5s timeout, no pings without streams.
func ClientKeepaliveParams() keepalive.ClientParameters {
	return keepalive.ClientParameters{
		Time:    10 * time.Second,
		Timeout: 5 * time.Second,
	}
}

// serverKeepaliveMinTime is the server half of the same agreement: the
// shortest gap between two pings a State Store server accepts before it counts
// a strike. It is half the client's ping interval rather than a copy of it;
// ServerKeepaliveOption carries the arithmetic.
const serverKeepaliveMinTime = 5 * time.Second

// ServerKeepaliveOption returns the server keepalive enforcement matching
// Dial's clients: pings at least 5s apart are allowed while a stream is open.
func ServerKeepaliveOption() grpc.ServerOption {
	return grpc.KeepaliveEnforcementPolicy(ServerKeepalivePolicy())
}

// ServerKeepalivePolicy returns the policy ServerKeepaliveOption installs.
func ServerKeepalivePolicy() keepalive.EnforcementPolicy {
	return keepalive.EnforcementPolicy{
		MinTime:             serverKeepaliveMinTime,
		PermitWithoutStream: false,
	}
}

// TargetIsLoopback reports whether addr's host is a loopback IP or
// "localhost".
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

// WaitForPeer makes calls to the State Store wait for it to come up
// instead of failing while it is down or restarting. A call that must not
// wait carries its own deadline.
var WaitForPeer = grpc.WithDefaultCallOptions(grpc.WaitForReady(true))
