package servicekit

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/secret"
	"google.golang.org/grpc"
)

// StateStoreClient dials the State Store gRPC service the daemon's own
// capture/mapping/binding consumers call. Token resolution is the daemon's
// (it owns the config keys and the secret registry); the dial itself and the
// fail-closed non-loopback rule belong to the transport, so they come
// from staterpc.Dial. Calls wait for the State Store rather than fail while it
// is down, so boot order between services does not matter.
func StateStoreClient(services config.Services, secrets *secret.Registry) (*staterpc.Client, func(), error) {
	target, err := services.RequireTarget(config.ServiceNameState)
	if err != nil {
		return nil, nil, err
	}
	return staterpc.Dial(target, services.ResolvedToken(config.ServiceNameState, secrets.Getenv), WaitForPeer)
}

// WaitForPeer makes a service's calls to another service wait for it to come
// up instead of failing while it is down or restarting. A call that must not
// wait carries its own deadline.
var WaitForPeer = grpc.WithDefaultCallOptions(grpc.WaitForReady(true))
