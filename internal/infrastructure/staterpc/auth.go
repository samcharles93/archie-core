package staterpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// tokenMetadataKey is the gRPC metadata key the bearer token travels in.
// Metadata, never a URL, per docs/prds/state-store-contract.md §9's token
// lifecycle note.
const tokenMetadataKey = "state-store-token"

// TokenValidator reports whether token is a currently-issued, unexpired
// bearer token for the State Store's bridge-address listener topology. The
// caller (the daemon) owns the issued-token set; this package only enforces
// the check server-side and attaches the token client-side.
type TokenValidator func(token string) bool

// UnaryTokenInterceptor validates the bearer token carried in each unary
// call's metadata, returning codes.Unauthenticated on a missing, unknown, or
// expired token. Required on the bridge-address (agent-consumed) listener
// topology per §9; not used on the loopback-only, daemon-only topology.
func UnaryTokenInterceptor(validate TokenValidator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := checkToken(ctx, validate); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func checkToken(ctx context.Context, validate TokenValidator) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing state store token")
	}
	values := md.Get(tokenMetadataKey)
	if len(values) == 0 || values[0] == "" {
		return status.Error(codes.Unauthenticated, "missing state store token")
	}
	if !validate(values[0]) {
		return status.Error(codes.Unauthenticated, "invalid state store token")
	}
	return nil
}

// UnaryClientTokenInterceptor attaches token to every outgoing call's
// metadata under tokenMetadataKey. One token covers a task's container
// lifetime (§9's per-incumbence lifecycle) so it does not need to be
// refreshed mid-connection. Used by the agent's long-lived client
// connection to the State Store's bridge-address listener.
func UnaryClientTokenInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), method, req, reply, cc, opts...)
	}
}

// StreamClientTokenInterceptor attaches token to the outgoing stream's
// context, covering the server-streaming read RPCs (StreamCaptures,
// StreamUndispatchedCaptures) the unary interceptor cannot see. Without it
// those calls reach a token-protected listener with no credential and the
// server's stream interceptor refuses them, leaving the capture batch reads
// -- the ones moved onto streams precisely so a large batch crosses -- with
// no working path.
func StreamClientTokenInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), desc, cc, method, opts...)
	}
}
