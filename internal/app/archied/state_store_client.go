package archied

import (
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// stateStoreResolvedToken returns the bearer token a State Store client
// presents: the explicit [services.state].target_token key, then the
// STATE_STORE_TOKEN secret/env. The daemon injects the same token into agent
// containers as STATE_STORE_TOKEN so the agent authenticates to the same
// remote store the daemon dials.
func stateStoreResolvedToken(settings config.ServiceConnection, secrets *secret.Registry) string {
	token := settings.TargetToken
	if token == "" {
		token = secrets.Getenv("STATE_STORE_TOKEN")
	}
	return token
}

// composeStateStoreClient dials the State Store gRPC service the daemon's own
// capture/mapping/binding consumers call. Token resolution is the daemon's
// (it owns the config keys and the secret registry); the dial itself and the
// fail-closed non-loopback rule (§9) belong to the transport, so they come
// from staterpc.Dial.
func composeStateStoreClient(settings config.ServiceConnection, secrets *secret.Registry) (*staterpc.Client, func(), error) {
	target := strings.TrimSpace(settings.Target)
	if target == "" {
		return nil, nil, fmt.Errorf("services.state.target is required")
	}
	return staterpc.Dial(target, stateStoreResolvedToken(settings, secrets))
}
