package staterpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/samcharles93/archie-core/internal/infrastructure/rpcidentity"
)

// tokenMetadataKey is the gRPC metadata key the bearer token travels in.
// Metadata, never a URL.
const tokenMetadataKey = "state-store-token"

func outgoing(ctx context.Context, caller, token string) context.Context {
	ctx = rpcidentity.Outgoing(ctx, caller)
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token)
	}
	return ctx
}

// UnaryClientCredentialInterceptor attaches caller and token to every
// outgoing call.
func UnaryClientCredentialInterceptor(caller, token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(outgoing(ctx, caller, token), method, req, reply, cc, opts...)
	}
}

// StreamClientCredentialInterceptor attaches caller and token to every
// outgoing stream.
func StreamClientCredentialInterceptor(caller, token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(outgoing(ctx, caller, token), desc, cc, method, opts...)
	}
}
