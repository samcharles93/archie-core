package staterpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// tokenMetadataKey is the gRPC metadata key the bearer token travels in.
// Metadata, never a URL.
const tokenMetadataKey = "state-store-token"

// UnaryClientTokenInterceptor attaches token to every outgoing call.
func UnaryClientTokenInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), method, req, reply, cc, opts...)
	}
}

// StreamClientTokenInterceptor attaches token to every outgoing stream.
func StreamClientTokenInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), desc, cc, method, opts...)
	}
}
