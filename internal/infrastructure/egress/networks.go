package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	// RelayAlias is the name a sandbox container reaches its relay by.
	RelayAlias = "archie-egress"
	// RelayPort is the port the relay forwards to the egress proxy.
	RelayPort = 3128
	// RelayNATSPort and RelayStateStorePort are the ports the relay forwards
	// to NATS and the State Store, for the worker in a sandbox container.
	RelayNATSPort       = 4222
	RelayStateStorePort = 50051

	relayLabel   = "archie-egress-relay"
	sandboxLabel = "archie-egress-sandbox"
)

// RelaySpec is how the relay container is started. Network is the
// host-reachable network it joins to reach the proxy; Entrypoint and Cmd
// run archie-agent relay forwarding to the proxy's address on that network.
type RelaySpec struct {
	// Name is the relay container's name. It also scopes the labels Start
	// prunes by, so two Networks on one host never remove each other's.
	Name       string
	Image      string
	Entrypoint []string
	Cmd        []string
	Network    string
	// Binds are host paths in Docker's "src:dst[:opts]" form. The relay
	// needs none when its image carries archie-agent.
	Binds []string
}

// Networks provisions sandbox networks: internal bridges on which the host
// holds no address, so the only reachable endpoint is the relay. Every
// other host service, including ones listening on all interfaces, is out of
// reach.
type Networks struct {
	cli   *client.Client
	relay RelaySpec
}

func NewNetworks(cli *client.Client, relay RelaySpec) *Networks {
	return &Networks{cli: cli, relay: relay}
}

// Start removes sandbox networks a previous daemon left behind and starts a
// fresh relay. Work interrupted by a restart is requeued, so no live sandbox
// depends on the old ones.
func (n *Networks) Start(ctx context.Context) error {
	if err := n.removeRelay(ctx); err != nil {
		return err
	}
	list, err := n.cli.NetworkList(ctx, client.NetworkListOptions{Filters: client.Filters{}.Add("label", sandboxLabel+"="+n.relay.Name)})
	if err != nil {
		return fmt.Errorf("list sandbox networks: %w", err)
	}
	for _, stale := range list.Items {
		if _, err := n.cli.NetworkRemove(ctx, stale.ID, client.NetworkRemoveOptions{}); err != nil {
			return fmt.Errorf("remove stale sandbox network %s: %w", stale.Name, err)
		}
	}
	created, err := n.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: n.relay.Name,
		Config: &container.Config{
			Image:      n.relay.Image,
			Entrypoint: n.relay.Entrypoint,
			Cmd:        n.relay.Cmd,
			Labels:     map[string]string{relayLabel: n.relay.Name},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   container.NetworkMode(n.relay.Network),
			Binds:         n.relay.Binds,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		},
	})
	if err != nil {
		return fmt.Errorf("create egress relay: %w", err)
	}
	if _, err := n.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return errors.Join(fmt.Errorf("start egress relay: %w", err), n.removeRelay(context.WithoutCancel(ctx)))
	}
	return nil
}

// Close removes the relay. Sandbox networks are removed by their owners.
func (n *Networks) Close(ctx context.Context) error { return n.removeRelay(ctx) }

func (n *Networks) removeRelay(ctx context.Context) error {
	list, err := n.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", relayLabel+"="+n.relay.Name),
	})
	if err != nil {
		return fmt.Errorf("list egress relays: %w", err)
	}
	for _, c := range list.Items {
		if _, err := n.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove egress relay: %w", err)
		}
	}
	return nil
}

// Create makes one sandbox's network and attaches the relay to it.
func (n *Networks) Create(ctx context.Context, name string) error {
	if _, err := n.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver:   "bridge",
		Internal: true,
		// With no host address on the bridge, nothing on the host is
		// routable from the sandbox; internal alone still exposes every
		// host service through the gateway.
		Options: map[string]string{"com.docker.network.bridge.inhibit_ipv4": "true"},
		Labels:  map[string]string{sandboxLabel: n.relay.Name},
	}); err != nil {
		return fmt.Errorf("create sandbox network %s: %w", name, err)
	}
	if _, err := n.cli.NetworkConnect(ctx, name, client.NetworkConnectOptions{
		Container:      n.relay.Name,
		EndpointConfig: &network.EndpointSettings{Aliases: []string{RelayAlias}},
	}); err != nil {
		return errors.Join(fmt.Errorf("attach egress relay to %s: %w", name, err), n.Remove(context.WithoutCancel(ctx), name))
	}
	return nil
}

// Remove detaches the relay from a sandbox network and deletes it.
func (n *Networks) Remove(ctx context.Context, name string) error {
	_, detachErr := n.cli.NetworkDisconnect(ctx, name, client.NetworkDisconnectOptions{Container: n.relay.Name, Force: true})
	if _, err := n.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil {
		return errors.Join(detachErr, fmt.Errorf("remove sandbox network %s: %w", name, err))
	}
	return nil
}

// ProxyEnv is the environment a sandbox container needs to send its egress
// through the relay under session token and to trust the daemon CA mounted
// at caPath. Both spellings are set because tools disagree on case.
func ProxyEnv(token, caPath string) []string {
	proxy := (&url.URL{
		Scheme: "http",
		User:   url.UserPassword(proxyUser, token),
		Host:   net.JoinHostPort(RelayAlias, strconv.Itoa(RelayPort)),
	}).String()
	env := []string{"NO_PROXY=", "no_proxy="}
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		env = append(env, name+"="+proxy, strings.ToLower(name)+"="+proxy)
	}
	for _, name := range []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"} {
		env = append(env, name+"="+caPath)
	}
	return env
}

// RelayArgs is archie-agent relay's arguments forwarding the relay's fixed
// ports to the proxy, NATS and the State Store at their host-reachable
// addresses. An empty address is not forwarded.
func RelayArgs(proxy, nats, stateStore string) []string {
	args := []string{"relay"}
	for port, target := range map[int]string{RelayPort: proxy, RelayNATSPort: nats, RelayStateStorePort: stateStore} {
		if target != "" {
			args = append(args, "-forward", ":"+strconv.Itoa(port)+"="+target)
		}
	}
	return args
}
