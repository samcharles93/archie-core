package egress

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/sandbox-kit-spec/v3/spec"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const probeImage = "alpine:3"

func buildStatic(t *testing.T, dir, pkg, name string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, name), pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, out)
	}
}

func ensureImage(t *testing.T, cli *client.Client, ref string) {
	t.Helper()
	if _, err := cli.ImageInspect(t.Context(), ref); err == nil {
		return
	}
	rc, err := cli.ImagePull(t.Context(), ref, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
}

func bridgeGateway(t *testing.T, cli *client.Client) string {
	t.Helper()
	res, err := cli.NetworkInspect(t.Context(), "bridge", client.NetworkInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Network.IPAM.Config {
		if c.Gateway.IsValid() && c.Gateway.Is4() {
			return c.Gateway.String()
		}
	}
	t.Fatal("default bridge has no IPv4 gateway")
	return ""
}

// runProbe runs egressclient in a container on the sandbox network and
// returns one result line per probe.
func runProbe(t *testing.T, cli *client.Client, networkName, bin, caPath, token string, probes ...string) []string {
	t.Helper()
	outDir := t.TempDir()
	if err := os.Chmod(outDir, 0o777); err != nil {
		t.Fatal(err)
	}
	created, err := cli.ContainerCreate(t.Context(), client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      probeImage,
			Entrypoint: []string{"/probe/egressclient"},
			Cmd:        append([]string{"/out/result"}, probes...),
			Env:        ProxyEnv(token, "/ca/ca.pem"),
		},
		HostConfig: &container.HostConfig{
			NetworkMode: container.NetworkMode(networkName),
			// z relabels for SELinux hosts, which otherwise refuse the
			// container access to freshly created host files.
			Binds: []string{bin + ":/probe:ro,z", caPath + ":/ca:ro,z", outDir + ":/out:z"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = cli.ContainerRemove(context.WithoutCancel(t.Context()), created.ID, client.ContainerRemoveOptions{Force: true})
	}()
	if _, err := cli.ContainerStart(t.Context(), created.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	wait := cli.ContainerWait(t.Context(), created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case <-wait.Result:
	case err := <-wait.Error:
		t.Fatal(err)
	case <-time.After(90 * time.Second):
		t.Fatal("probe container did not finish")
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "result"))
	if err != nil {
		t.Fatalf("probe wrote no result: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// TestSandboxNetworkReachesOnlyTheProxy runs real containers: a sandbox on
// an isolated network, the relay, and the proxy on the host. It proves the
// PRD's egress properties end to end rather than by construction.
func TestSandboxNetworkReachesOnlyTheProxy(t *testing.T) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	ensureImage(t, cli, probeImage)

	bin := t.TempDir()
	buildStatic(t, bin, "github.com/samcharles93/archie-core/cmd/archie-agent", "archie-agent")
	buildStatic(t, bin, "./testdata/egressclient", "egressclient")

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "upstream:"+r.Host+r.URL.Path)
	}))
	t.Cleanup(upstream.Close)
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())

	caDir := t.TempDir()
	ca, err := LoadOrCreateCA(caDir)
	if err != nil {
		t.Fatal(err)
	}
	proxy := NewProxy(ca, ProxyOptions{
		UpstreamRoots: roots,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != "api.example.com:443" {
				return nil, fmt.Errorf("unrouted upstream %s", addr)
			}
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		},
	})
	gateway := bridgeGateway(t, cli)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", net.JoinHostPort(gateway, "0"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })
	proxyAddr := ln.Addr().String()

	name := fmt.Sprintf("archie-egress-test-%d", time.Now().UnixNano())
	networks := NewNetworks(cli, RelaySpec{
		Name:       name + "-relay",
		Image:      probeImage,
		Entrypoint: []string{"/bin/archie-agent"},
		Cmd:        RelayArgs(proxyAddr, "", ""),
		Network:    "bridge",
		Binds:      []string{filepath.Join(bin, "archie-agent") + ":/bin/archie-agent:ro,z"},
	})
	if err := networks.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = networks.Close(context.WithoutCancel(t.Context())) })
	if err := networks.Create(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = networks.Remove(context.WithoutCancel(t.Context()), name) })

	// A host service listening on every interface, the way NATS and
	// Postgres do. The sandbox must not reach it by any address.
	hostService, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostService.Close() })
	go func() {
		for {
			c, err := hostService.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, hostPort, err := net.SplitHostPort(hostService.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sandboxNet, err := cli.NetworkInspect(t.Context(), name, client.NetworkInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sandboxNet.Network.IPAM.Config) == 0 || !sandboxNet.Network.IPAM.Config[0].Subnet.IsValid() {
		t.Fatal("sandbox network has no subnet")
	}
	// The address a bridge gateway would hold: the subnet's first host. An
	// isolated sandbox network leaves it unassigned.
	sandboxGateway := sandboxNet.Network.IPAM.Config[0].Subnet.Masked().Addr().Next().String()

	session, err := proxy.Register(SessionOptions{Run: "integration", Network: &spec.PhasedNetwork{Runtime: &spec.NetworkRules{Allow: []string{"api.example.com:443"}}}})
	if err != nil {
		t.Fatal(err)
	}
	session.EnterRuntime()

	results := runProbe(t, cli, name, bin, caDir, session.Token(),
		"get", "https://api.example.com/ok",
		"get", "https://denied.example.com/",
		"dial", proxyAddr,
		"dial", net.JoinHostPort(sandboxGateway, hostPort),
		"dial", net.JoinHostPort(gateway, hostPort),
		"dial", "1.1.1.1:443",
	)
	want := []struct{ prefix, why string }{
		{"OK 200 upstream:api.example.com/ok", "an allowed host is reachable through the relay and proxy"},
		{"ERR", "a host outside the policy is refused"},
		{"BLOCKED", "the proxy's own listener is unreachable except through the relay"},
		{"BLOCKED", "a host service on all interfaces is unreachable through the sandbox's own gateway address"},
		{"BLOCKED", "a host service on all interfaces is unreachable through the docker bridge"},
		{"BLOCKED", "the internet is unreachable directly"},
	}
	if len(results) != len(want) {
		t.Fatalf("probe returned %d results, want %d: %q", len(results), len(want), results)
	}
	for i, w := range want {
		if !strings.HasPrefix(results[i], w.prefix) {
			t.Errorf("%s: got %q, want prefix %q", w.why, results[i], w.prefix)
		}
	}

	wrong := runProbe(t, cli, name, bin, caDir, "not-a-token", "get", "https://api.example.com/ok")
	if len(wrong) != 1 || !strings.HasPrefix(wrong[0], "ERR") {
		t.Errorf("a sandbox with the wrong token reached upstream: %q", wrong)
	}
}
