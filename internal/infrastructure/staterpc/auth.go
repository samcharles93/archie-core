package staterpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// tokenMetadataKey is the gRPC metadata key the bearer token travels in.
// Metadata, never a URL.
const tokenMetadataKey = "state-store-token"

// callerMetadataKey names the service making the call.
const callerMetadataKey = "state-store-caller"

// principalMetadataKey carries the identity a service acts for, when it acts
// for one.
const principalMetadataKey = "state-store-principal"

// outgoing stamps the caller's service name, the principal on ctx when there
// is one, and the token when set.
func outgoing(ctx context.Context, caller, token string) context.Context {
	pairs := []string{callerMetadataKey, caller}
	if p, ok := access.PrincipalFromContext(ctx); ok {
		pairs = append(pairs, principalMetadataKey, string(p.IdentityID))
	}
	if token != "" {
		pairs = append(pairs, tokenMetadataKey, token)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
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

// Callers resolves who a call is made by. It runs after authentication,
// which is what makes the metadata trustworthy: only holders of the instance
// credential reach it with their metadata intact (a task grant's is
// stripped). A call naming a principal acts in that principal's org; any
// other acts in the default org.
type Callers struct {
	Principals access.PrincipalSource
}

func (c Callers) resolve(ctx context.Context) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	caller := first(md, callerMetadataKey)
	if caller != "" {
		ctx = access.WithActor(access.WithSource(ctx, caller), caller)
	}
	id := first(md, principalMetadataKey)
	if id == "" {
		return ctx, nil
	}
	if c.Principals == nil {
		return nil, status.Error(codes.Unavailable, "principals unavailable")
	}
	p, err := c.Principals.PrincipalFor(ctx, identity.IdentityID(id))
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "resolve principal: %v", err)
	}
	return org.WithOrg(access.WithActor(access.WithPrincipal(ctx, p), id), p.Org), nil
}

// Unary attributes unary calls.
func (c Callers) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := c.resolve(ctx)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// Stream attributes streams.
func (c Callers) Stream() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := c.resolve(stream.Context())
		if err != nil {
			return err
		}
		return handler(srv, attributedStream{ServerStream: stream, ctx: ctx})
	}
}

type attributedStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // grpc.ServerStream exposes its context only through Context()
}

func (s attributedStream) Context() context.Context { return s.ctx }

func first(md metadata.MD, key string) string {
	if values := md.Get(key); len(values) > 0 {
		return values[0]
	}
	return ""
}
