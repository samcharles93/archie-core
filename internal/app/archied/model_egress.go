package archied

import (
	"fmt"
	"maps"
	"slices"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
	"github.com/samcharles93/archie-core/internal/storage"
)

const modelProxyCAPath = "/opt/archie/egress-ca.pem"

// modelEgress opens native task containers' model sessions on the egress
// proxy. Each keyed provider's key is the org's credential binding named
// after the provider.
type modelEgress struct {
	proxy  *egress.Proxy
	addr   string
	caFile string
	config *config.Holder
}

func (m modelEgress) Open(token, org string, providers map[string]config.Provider) (daemon.ModelSession, error) {
	var keyed []string
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		if providers[name].APIKeyEnv != "" {
			keyed = append(keyed, name)
		}
	}
	if len(keyed) == 0 {
		return daemon.ModelSession{}, nil
	}
	bound := m.config.Get().Containers.BoundCredentials(org, keyed, keyed)
	var (
		subs  []egress.Substitution
		hosts []string
		env   []string
	)
	for _, name := range keyed {
		p := providers[name]
		if _, ok := bound[name]; !ok {
			return daemon.ModelSession{}, fmt.Errorf("provider %q has no credential binding named %q in org %s", name, name, org)
		}
		host, err := egress.ProviderHost(p.Class, p.BaseURL)
		if err != nil {
			return daemon.ModelSession{}, fmt.Errorf("provider %q: %w", name, err)
		}
		subs = append(subs, egress.Substitution{Service: name, Host: host})
		hosts = append(hosts, host)
		env = append(env, p.APIKeyEnv+"="+egress.SentinelFor(name))
	}
	session, err := m.proxy.Register(egress.SessionOptions{
		Token: token, Org: org, Substitutions: subs,
		Network: &spec.PhasedNetwork{Runtime: &spec.NetworkRules{Allow: hosts}},
	})
	if err != nil {
		return daemon.ModelSession{}, err
	}
	session.EnterRuntime()
	return daemon.ModelSession{
		Env:    append(env, egress.ModelProxyEnvFor(m.addr, token, hosts, modelProxyCAPath)...),
		Mounts: []storage.Mount{{Type: storage.MountTypeBind, Source: m.caFile, Destination: modelProxyCAPath}},
	}, nil
}

func (m modelEgress) Close(token string) { m.proxy.Revoke(token) }
