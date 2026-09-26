package kitrun

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

// Endpoints are the host-reachable addresses a Kit task's worker needs,
// with the task's own credentials for them.
type Endpoints struct {
	NATS            string
	NATSToken       string
	StateStore      string
	StateStoreToken string
}

// WorkerEnv is the worker's entire environment in a Kit container: NATS and
// the State Store at the relay's fixed ports, and the task's credentials.
// Nothing else crosses, so no model provider key enters the container.
func WorkerEnv(e Endpoints, uid, gid int) ([]string, error) {
	u, err := url.Parse(e.NATS)
	if err != nil || u.Scheme == "" {
		return nil, fmt.Errorf("nats url %q: want scheme://host:port", e.NATS)
	}
	env := []string{"NATS_URL=" + u.Scheme + "://" + relayAddr(egress.RelayNATSPort)}
	if e.NATSToken != "" {
		env = append(env, "NATS_TOKEN="+e.NATSToken)
	}
	if e.StateStore != "" {
		env = append(env, "STATE_STORE_URL="+relayAddr(egress.RelayStateStorePort))
		if e.StateStoreToken != "" {
			env = append(env, "STATE_STORE_TOKEN="+e.StateStoreToken)
		}
	}
	return append(env, "WORKTREE_UID="+strconv.Itoa(uid), "WORKTREE_GID="+strconv.Itoa(gid)), nil
}

// relayTargets are the host:port addresses the relay forwards NATS and the
// State Store to.
func relayTargets(e Endpoints) (nats, stateStore string, err error) {
	if strings.Contains(e.NATS, ",") {
		return "", "", fmt.Errorf("nats url %q lists several servers; the relay forwards one", e.NATS)
	}
	u, err := url.Parse(e.NATS)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("nats url %q: want scheme://host:port", e.NATS)
	}
	stateStore = e.StateStore
	if _, rest, ok := strings.Cut(stateStore, ":///"); ok {
		stateStore = rest
	}
	return u.Host, stateStore, nil
}

func relayAddr(port int) string {
	return net.JoinHostPort(egress.RelayAlias, strconv.Itoa(port))
}

// RelayCommand is the relay's archie-agent arguments, forwarding its fixed
// ports to the proxy and to the daemon's NATS and State Store.
func RelayCommand(proxy string, e Endpoints) ([]string, error) {
	nats, stateStore, err := relayTargets(e)
	if err != nil {
		return nil, err
	}
	return egress.RelayArgs(proxy, nats, stateStore), nil
}
