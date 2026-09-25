package agentworker

import (
	"context"
	"net"

	"github.com/samcharles93/archie-core/internal/infrastructure/egress/relay"
)

// RunRelay listens on listen and forwards every connection to target, the
// egress proxy, until ctx is cancelled.
func RunRelay(ctx context.Context, listen, target string) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return err
	}
	return relay.Serve(ctx, ln, target)
}
