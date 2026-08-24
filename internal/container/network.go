package container

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

// NetworkName is the Docker network managed worker containers join.
func (p *Pool) NetworkName() string { return p.network }

// HostGateway is the host-side IPv4 gateway of NetworkName. It is non-empty
// only when Config.RequireHostGateway requested embedded broker reachability.
func (p *Pool) HostGateway() string { return p.hostGateway }

func resolveHostGateway(
	ctx context.Context,
	cli *client.Client,
	networkName string,
	required bool,
) (string, error) {
	if !required {
		return "", nil
	}
	result, err := cli.NetworkInspect(ctx, networkName, client.NetworkInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect Docker network %q for embedded NATS: %w", networkName, err)
	}
	network := result.Network.Network
	if network.Driver != "bridge" {
		return "", fmt.Errorf("docker network %q uses driver %q, want bridge for embedded NATS", networkName, network.Driver)
	}
	if network.Scope != "" && network.Scope != "local" {
		return "", fmt.Errorf("docker network %q has scope %q, want local for embedded NATS", networkName, network.Scope)
	}
	for _, ipam := range network.IPAM.Config {
		gateway := ipam.Gateway
		if gateway.IsValid() && gateway.Is4() && !gateway.IsUnspecified() {
			return gateway.String(), nil
		}
	}
	return "", fmt.Errorf("docker network %q has no usable IPv4 host gateway for embedded NATS", networkName)
}
