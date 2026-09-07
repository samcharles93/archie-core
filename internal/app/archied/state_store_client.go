package archied

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// composeStateStoreClient dials the State Store gRPC service the daemon's own
// capture/mapping/binding consumers will call when [services.state].target is
// set. It mirrors the gateway's composeChatContract (internal/app/archied/
// chat_service.go), but the fail-closed rule for non-loopback targets (§9) is
// the State Store's own and requires a bearer token. The token comes from
// [services.state].target_token, falling back to the STATE_STORE_TOKEN
// secret/env. The returned cleanup closes the connection; Close() on the
// adapter itself is a no-op, because the store service owns its own DB
// lifecycle (§11).
// stateStoreResolvedToken returns the bearer token a State Store client
// presents, matching composeStateStoreClient's resolution order: the explicit
// [services.state].target_token key, then the STATE_STORE_TOKEN secret/env. The
// daemon injects the same token into agent containers as STATE_STORE_TOKEN so
// the agent authenticates to the same remote store the daemon dials.
func stateStoreResolvedToken(settings config.ServiceConnection, secrets *secret.Registry) string {
	token := settings.TargetToken
	if token == "" {
		token = secrets.Getenv("STATE_STORE_TOKEN")
	}
	return token
}

func composeStateStoreClient(settings config.ServiceConnection, secrets *secret.Registry) (*staterpc.Client, func(), error) {
	target := strings.TrimSpace(settings.Target)
	if target == "" {
		return nil, nil, fmt.Errorf("services.state.target is required")
	}
	token := stateStoreResolvedToken(settings, secrets)
	loopback, err := stateStoreListenIsLoopback(target)
	if err != nil {
		return nil, nil, fmt.Errorf("services.state.target must be host:port: %w", err)
	}
	if !loopback && token == "" {
		return nil, nil, fmt.Errorf(
			"services.state.target %q is non-loopback; non-loopback exposure requires a bearer token ([services.state].target_token / STATE_STORE_TOKEN) or TLS, mirroring the gateway's --listen confinement (docs/prds/state-store-contract.md §9)",
			target,
		)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(staterpc.UnaryClientTokenInterceptor(token)))
	}
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create state store client: %w", err)
	}
	return staterpc.NewClient(conn), func() { _ = conn.Close() }, nil
}
