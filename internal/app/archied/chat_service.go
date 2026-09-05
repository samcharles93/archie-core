package archied

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
)

// composeChatContract dials the standalone archie-gateway, the sole owner of
// ChatContract.
func composeChatContract(settings config.ServiceConnection, options ...grpc.DialOption) (gateway.ChatContract, func(), error) {
	if settings.Mode != "remote" {
		return nil, nil, fmt.Errorf("unsupported gateway service mode %q; archie-gateway is required, see deployments/README.md", settings.Mode)
	}
	if strings.TrimSpace(settings.Target) == "" {
		return nil, nil, fmt.Errorf("remote gateway requires a target")
	}
	// Deployments that put the Gateway on a separate trust boundary supply
	// their own transport credentials through options; the default must be
	// usable without manufacturing a certificate in the config loader.
	opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, options...)
	conn, err := grpc.NewClient(settings.Target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create gateway client: %w", err)
	}
	return gatewayrpc.NewClient(conn), func() { _ = conn.Close() }, nil
}
