package gatewayrpc

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial returns a chat contract client for target, the standalone
// archie-gateway process. Deployments that put the Gateway on a separate
// trust boundary supply their own transport credentials through options; the
// default must be usable without manufacturing a certificate in the config
// loader. The returned cleanup closes the connection.
//
// The Gateway listener is loopback-only (internal/app/archied.RunGateway), so
// a caller is colocated with it until a Gateway transport-security amendment
// says otherwise.
func Dial(target string, options ...grpc.DialOption) (*Client, func(), error) {
	if strings.TrimSpace(target) == "" {
		return nil, nil, fmt.Errorf("gateway target is required")
	}
	opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, options...)
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create gateway client: %w", err)
	}
	return NewClient(conn), func() { _ = conn.Close() }, nil
}
