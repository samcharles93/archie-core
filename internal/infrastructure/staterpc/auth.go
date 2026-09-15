package staterpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// tokenMetadataKey is the gRPC metadata key the bearer token travels in.
// Metadata, never a URL, per docs/prds/state-store-contract.md §9's token
// lifecycle note.
const tokenMetadataKey = "state-store-token"

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
