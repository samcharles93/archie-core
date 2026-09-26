package archied

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/sandbox-kit-spec/v3/fetch"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
	"github.com/samcharles93/archie-core/internal/infrastructure/kitrun"
)

const relayName = "archie-egress-relay"

// setupKitLauncher builds the egress path Kit tasks run on: the per-daemon
// CA, the proxy on the worker bridge's host gateway, and the relay. It
// degrades to no launcher, which parks Kit tasks, rather than failing boot:
// image profiles do not need it.
func (b *boot) setupKitLauncher(ctx context.Context) {
	pool, log := b.containerPool, b.log
	if pool == nil || pool.HostGateway() == "" {
		log.Info("kit harness runs disabled: they need the managed container pool on a bridge with a host gateway")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Warn("kit harness runs disabled", "err", err)
		return
	}
	agentBinary := filepath.Join(filepath.Dir(exe), "archie-agent")
	caDir := filepath.Join(b.cfg.StateDir, "egress")
	ca, err := egress.LoadOrCreateCA(caDir)
	if err != nil {
		log.Warn("kit harness runs disabled: egress CA", "err", err)
		return
	}
	fetcher, err := fetch.New(fetch.WithDockerCredentials())
	if err != nil {
		log.Warn("kit harness runs disabled: kit registry client", "err", err)
		return
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(pool.HostGateway(), "0"))
	if err != nil {
		log.Warn("kit harness runs disabled: egress proxy listener", "err", err)
		return
	}
	endpoints := kitrun.Endpoints{NATS: b.natsURL, StateStore: strings.TrimSpace(b.cfg.Services.Get(config.ServiceNameState).Target)}
	cmd, err := kitrun.RelayCommand(ln.Addr().String(), endpoints)
	if err != nil {
		_ = ln.Close()
		log.Warn("kit harness runs disabled: egress relay", "err", err)
		return
	}
	// No credential resolver yet (archie-core-egkf.11): a Kit's required
	// credential is refused at the proxy until run credentials carry secrets.
	proxy := egress.NewProxy(ca, egress.ProxyOptions{})
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("egress proxy stopped", "err", err)
		}
	}()
	networks := egress.NewNetworks(pool.Client(), egress.RelaySpec{
		Name: relayName, Image: b.cfg.Containers.Image,
		Entrypoint: []string{"archie-agent"}, Cmd: cmd, Network: pool.NetworkName(),
	})
	b.addCleanup(shutdownEgress(networks, srv, log))
	b.kitLauncher = &kitrun.Launcher{
		Pool: pool, Fetch: fetcher, Proxy: proxy, Networks: networks,
		AgentBinary: agentBinary, CAFile: egress.CACertPath(caDir),
	}
	log.Info("kit harness runs enabled", "proxy", ln.Addr().String())
}

// shutdownEgress removes the relay and stops the proxy. It derives its own
// budget because the boot context is cancelled by the time shutdown runs.
//
//nolint:contextcheck
func shutdownEgress(networks *egress.Networks, srv *http.Server, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := networks.Close(stopCtx); err != nil {
			log.Warn("egress relay removal failed", "err", err)
		}
		_ = srv.Close()
	}
}
