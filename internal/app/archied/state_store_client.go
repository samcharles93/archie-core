package archied

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// composeStateStoreClient dials the State Store gRPC service the daemon's own
// capture/mapping/binding consumers call. Token resolution is the daemon's
// (it owns the config keys and the secret registry); the dial itself and the
// fail-closed non-loopback rule (§9) belong to the transport, so they come
// from staterpc.Dial.
func composeStateStoreClient(services config.Services, secrets *secret.Registry) (*staterpc.Client, func(), error) {
	target, err := services.RequireTarget(config.ServiceNameState)
	if err != nil {
		return nil, nil, err
	}
	return staterpc.Dial(target, services.ResolvedToken(config.ServiceNameState, secrets.Getenv))
}
