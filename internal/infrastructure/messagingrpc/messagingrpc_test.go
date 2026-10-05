package messagingrpc

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRequireToken(t *testing.T) {
	ok := func(context.Context, any) (any, error) { return "served", nil }
	for _, tc := range []struct {
		name string
		md   metadata.MD
		want codes.Code
	}{
		{"valid", metadata.Pairs(tokenMetadataKey, "secret"), codes.OK},
		{"wrong", metadata.Pairs(tokenMetadataKey, "secrex"), codes.Unauthenticated},
		{"missing", metadata.MD{}, codes.Unauthenticated},
		{"other service's key", metadata.Pairs("gateway-token", "secret"), codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), tc.md)
			_, err := requireToken("secret")(ctx, nil, &grpc.UnaryServerInfo{}, ok)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("code = %v, want %v", got, tc.want)
			}
		})
	}
	if _, err := ServerOptions("0.0.0.0:8586", ""); err == nil {
		t.Fatal("non-loopback listen with no token was accepted")
	}
}
