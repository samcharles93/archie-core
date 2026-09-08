package gatewayrpc

import (
	"context"
	"crypto/subtle"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// tokenMetadataKey is the gRPC metadata key the Gateway Bearer [REDACTED]
// travels in. Metadata, never a URL, mirroring the State Store's
// token lifecycle (docs/prds/state-store-contract.md §9).
const tokenMetadataKey = "gateway-token"

// TargetIsLoopback reports whether addr's host is a loopback address. Both
// literal loopback IPs (127.0.0.1, ::1) and the "localhost" hostname count;
// everything else (including the wildcard 0.0.0.0 and DNS names) is treated
// as network-reachable and therefore non-loopback. It applies to a listen
// address and a dial target alike -- the same host decides both. It mirrors
// staterpc.TargetIsLoopback; the two transports keep their own copy so
// neither adapter imports the other.
func TargetIsLoopback(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, err
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

// validToken compares candidate against the configured token in constant
// time so a timing side channel cannot leak how many bytes matched.
func validToken(token, candidate string) bool {
	if token == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1
}

// UnaryServerInterceptor validates the Bearer [REDACTED] carried in each unary
// call's metadata, returning codes.Unauthenticated on a missing or wrong
// token. It is installed only for a non-loopback listener; a loopback
// listener serves with no interceptors.
func UnaryServerInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing gateway token")
		}
		values := md.Get(tokenMetadataKey)
		if len(values) == 0 || !validToken(token, values[0]) {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid gateway token")
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is the streaming counterpart of
// UnaryServerInterceptor: the chat Stream RPC is server-streaming, so its
// credential must be checked on the stream context as well.
func StreamServerInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		values := md.Get(tokenMetadataKey)
		if len(values) == 0 || !validToken(token, values[0]) {
			return status.Error(codes.Unauthenticated, "missing or invalid gateway token")
		}
		return handler(srv, stream)
	}
}

// UnaryClientTokenInterceptor attaches token to every outgoing unary call's
// metadata under tokenMetadataKey.
func UnaryClientTokenInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), method, req, reply, cc, opts...)
	}
}

// StreamClientTokenInterceptor attaches token to the outgoing stream's
// context, covering the chat Stream RPC that the unary interceptor cannot
// see.
func StreamClientTokenInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, tokenMetadataKey, token), desc, cc, method, opts...)
	}
}
