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
		grpc.WithKeepaliveParams(ClientKeepaliveParams()),
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

// ClientKeepaliveParams are the client keepalive settings Dial installs; see
// Dial's option comment for why each value is what it is. PermitWithoutStream
// is deliberately left false (the library default): the pings this dial needs
// are covered by an active stream, and pinging a connection with no streams is
// what a peer's keepalive enforcement policy records as a ping strike --
// against the State Store's default policy a streamless ping draws GOAWAY
// too_many_pings and would churn connections that are healthy today.
//
// It is the dialer's half of the keepalive agreement with
// ServerKeepalivePolicy, so it is exported: the side that installs a server
// option reads the interval this dialer actually pings at from here rather
// than restating it.
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

// ServerKeepaliveOption returns the server half of the keepalive agreement
// Dial's clients enter, for the composition that serves the State Store
// contract to install on its grpc.Server (internal/app/archied's
// stateStoreServerOpts installs it on every listener).
//
// The two sides have to agree on one number. A ping arriving less than
// EnforcementPolicy.MinTime after the previous ping is a strike, and the server
// answers the third strike with GOAWAY too_many_pings (maxPingStrikes is 2; a
// write on that transport is what resets the counter). grpc's default policy
// MinTime is 5 minutes while the client above pings every 10 seconds, so a
// connection that carries no write for ~40s -- which is what a watch is while
// the watched resource's version is unchanged -- is torn down where before the
// client keepalive existed it stayed up. A process that writes on the same
// transport every 30s (the apply-status re-stamp, applystatus.RestampInterval)
// resets the counter often enough to hide this; an idle connection is what it
// kills, and a keepalive that disconnects is worse than no keepalive.
//
// MinTime is 5s, half the client's ping interval. The client's interval is the
// larger of its Time and grpc's KeepaliveMinPingTime clamp -- 10s, so no caller
// can ask for faster pings -- and its keepalive loop arms its timer Time ahead
// and re-arms it only later, on read activity, so consecutive pings are never
// closer than ~10s apart. A 5s floor thus separates every ping from a strike by
// a factor of two, worth more than Go's timer granularity and any scheduling
// delay, which can only push pings further apart. A MinTime of 10s would sit
// exactly on the boundary the clamp already enforces, where a ping a
// microsecond early is a strike; 5 minutes is the defect itself.
//
// The floor is not a blanket exemption: a ping arriving less than 5s after the
// previous one, and any ping on a connection with no stream, still strikes.
// PermitWithoutStream stays false, matching the client, which never pings a
// connection with no streams: every client this server has is a staterpc.Dial
// -- the daemon's control-plane client, the archie-ui dashboard, archie-
// messaging and the archie-agent container -- so permitting streamless pings
// would only relax a setting none of them exercises.
func ServerKeepaliveOption() grpc.ServerOption {
	return grpc.KeepaliveEnforcementPolicy(ServerKeepalivePolicy())
}

// ServerKeepalivePolicy returns the policy ServerKeepaliveOption installs as a
// value. A grpc.ServerOption is a closure over unexported grpc state with no
// accessor, so the option cannot be read back once built; this value is
// therefore the handle the agreement is held to -- a caller installing the
// option can assert the policy it is built from, and ClientKeepaliveParams
// gives the interval that policy must stay below.
func ServerKeepalivePolicy() keepalive.EnforcementPolicy {
	return keepalive.EnforcementPolicy{
		MinTime:             serverKeepaliveMinTime,
		PermitWithoutStream: false,
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
