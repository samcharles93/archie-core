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

// composeChatContract selects transport without involving network discovery
// in local calls. The returned cleanup belongs to the application lifecycle.
func composeChatContract(settings config.ServiceConnection, local gateway.ChatContract, options ...grpc.DialOption) (gateway.ChatContract, func(), error) {
	switch settings.Mode {
	case "", "inproc":
		if settings.Target != "" {
			return nil, nil, fmt.Errorf("gateway target requires remote mode")
		}
		return local, func() {}, nil
	case "remote":
		if strings.TrimSpace(settings.Target) == "" {
			return nil, nil, fmt.Errorf("remote gateway requires a target")
		}
		// The first extraction uses a loopback gRPC hop. Deployments that put
		// the Gateway on a separate trust boundary supply their own transport
		// credentials through options; the local process split must be usable
		// without manufacturing a certificate in the config loader.
		opts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, options...)
		conn, err := grpc.NewClient(settings.Target, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("create gateway client: %w", err)
		}
		return gatewayrpc.NewClient(conn), func() { _ = conn.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown gateway service mode %q", settings.Mode)
	}
}
