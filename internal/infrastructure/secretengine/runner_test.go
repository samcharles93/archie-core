package secretengine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretenginev1 "github.com/samcharles93/archie-core/internal/contracts/secretengine/v1"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/secret"
)

// echoEngine is the extension the test binary becomes when the host launches
// it: "ENV:NAME" resolves to that variable as the plugin process sees it, any
// other key to a value derived from the Configure settings.
type echoEngine struct {
	secretenginev1.UnimplementedSecretEngineServiceServer
	settings map[string]string
}

func (e *echoEngine) Configure(_ context.Context, r *secretenginev1.ConfigureRequest) (*secretenginev1.ConfigureResponse, error) {
	e.settings = r.GetSettings()
	return &secretenginev1.ConfigureResponse{}, nil
}

func (e *echoEngine) Resolve(ctx context.Context, r *secretenginev1.ResolveRequest) (*secretenginev1.ResolveResponse, error) {
	if name, ok := strings.CutPrefix(r.GetKey(), "ENV:"); ok {
		return &secretenginev1.ResolveResponse{Value: os.Getenv(name)}, nil
	}
	if url, ok := strings.CutPrefix(r.GetKey(), "GET:"); ok {
		return &secretenginev1.ResolveResponse{Value: fetch(url)}, nil
	}
	if addr, ok := strings.CutPrefix(r.GetKey(), "DIAL:"); ok {
		return &secretenginev1.ResolveResponse{Value: dial(ctx, addr)}, nil
	}
	if v, ok := e.settings[r.GetKey()]; ok {
		return &secretenginev1.ResolveResponse{Value: v}, nil
	}
	return nil, status.Error(codes.NotFound, "no such key")
}

// dial reports whether a raw TCP connection to addr succeeds.
func dial(ctx context.Context, addr string) string {
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return "dial failed"
	}
	_ = conn.Close()
	return "connected"
}

// fetch GETs target through the proxy HTTP_PROXY names, reporting the body or
// the failing status. The proxy is set explicitly because the environment
// lookup never proxies loopback, which is where the test servers listen.
func fetch(target string) string {
	proxy, err := url.Parse(os.Getenv("HTTP_PROXY"))
	if err != nil || proxy.Host == "" {
		return "no proxy"
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 5 * time.Second}
	resp, err := client.Get(target) //nolint:noctx // test plugin
	if err != nil {
		return "error: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.Status
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestMain(m *testing.M) {
	if os.Getenv(extension.Handshake.MagicCookieKey) != "" {
		goplugin.Serve(&goplugin.ServeConfig{
			HandshakeConfig: extension.Handshake,
			Plugins:         goplugin.PluginSet{Surface: &Plugin{Impl: &echoEngine{}}},
			GRPCServer:      goplugin.DefaultGRPCServer,
		})
		return
	}
	os.Exit(m.Run())
}

func selfDigest(t *testing.T) string {
	t.Helper()
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	return hex.EncodeToString(sum[:])
}

// lanAddress is a non-loopback IPv4 address of this machine. Without one the
// egress cases cannot run, and they fail rather than skip.
func lanAddress(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() {
			return ip.IP.String()
		}
	}
	t.Fatal("no non-loopback IPv4 address to stand in for an egress host")
	return ""
}

func TestHostLaunch(t *testing.T) {
	t.Setenv("ARCHIE_TEST_SECRET", "host-only")
	t.Setenv("ARCHIE_TEST_PASSED", "granted")
	good := selfDigest(t)
	// The egress gate refuses loopback, so the servers listen on this
	// machine's network address, standing in for a private-network host.
	lan := lanAddress(t)
	serve := func() string {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", net.JoinHostPort(lan, "0"))
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "reached") }))
		srv.Listener = ln
		srv.Start()
		t.Cleanup(srv.Close)
		return ln.Addr().String()
	}
	accepted, other := serve(), serve()
	loopbackServer := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(loopbackServer.Close)
	loopback := loopbackServer.Listener.Addr().String()

	tests := []struct {
		name    string
		sha     string
		key     string
		want    string
		wantErr string
		// open runs the package with no declared egress hosts.
		open bool
	}{
		{name: "an accepted egress host is reachable through the proxy", sha: good, key: "GET:http://" + accepted, want: "reached"},
		{name: "a host that was not accepted is refused", sha: good, key: "GET:http://" + other, want: "403 Forbidden"},
		{name: "a direct connection bypassing the proxy fails", sha: good, key: "DIAL:" + accepted, want: "dial failed"},
		{name: "a package declaring no egress hosts dials directly", sha: good, key: "DIAL:" + accepted, want: "connected", open: true},
		{name: "loopback is refused even when accepted", sha: good, key: "GET:http://" + loopback, want: "502 Bad Gateway"},
		{name: "settings reach the engine", sha: good, key: "color", want: "blue"},
		{name: "an unknown key is not found", sha: good, key: "missing", wantErr: "not found"},
		{name: "an env var outside the allowlist is invisible", sha: good, key: "ENV:ARCHIE_TEST_SECRET", want: ""},
		{name: "an allowlisted env var is passed", sha: good, key: "ENV:ARCHIE_TEST_PASSED", want: "granted"},
		{name: "a binary that does not match its digest is refused", sha: strings.Repeat("0", 64), wantErr: "checksum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := extension.NewHost(hclog.NewNullLogger())
			t.Cleanup(host.Close)
			spec := extension.Spec{Name: "echo", Path: os.Args[0], SHA256: tt.sha, Env: []string{"ARCHIE_TEST_PASSED"}, Egress: []string{accepted, loopback}}
			if tt.open {
				spec.Egress = nil
			}
			engine, err := Start(context.Background(), host, spec, map[string]string{"color": "blue"})
			if tt.sha != good {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Start error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := engine.Resolve(tt.key)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Resolve error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", tt.key, got, err, tt.want)
			}
		})
	}
}

type fakeSource struct {
	settings controlplanerpc.ExtensionSettings
	pkg      storepkg.Installed
}

func (f *fakeSource) Query(_ context.Context, _ string, decode func([]byte) error) (int64, bool, error) {
	raw, _ := json.Marshal(f.settings)
	return 1, true, decode(raw)
}

func (f *fakeSource) ListInstalled(context.Context) ([]storepkg.Installed, error) {
	listed := f.pkg
	listed.Layer = nil
	return []storepkg.Installed{listed}, nil
}

func (f *fakeSource) GetInstalled(context.Context, string) (storepkg.Installed, error) {
	return f.pkg, nil
}

func layerOf(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// An extension runs only while its package is accepted and the settings enable
// it, and stops the moment either stops being true.
func TestRunnerRunsOnlyWhatIsAcceptedAndEnabled(t *testing.T) {
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{pkg: storepkg.Installed{
		Name: "echo", Digest: "sha256:" + strings.Repeat("a", 64),
		Descriptor: storepkg.Descriptor{Contributes: storepkg.Contributions{
			Extensions: []storepkg.Extension{{Surface: storepkg.SurfaceSecretEngine, Path: "bin/echo"}},
		}},
		Layer: layerOf(t, "bin/echo", binary),
	}}
	registry := secret.NewRegistry()
	host := extension.NewHost(hclog.NewNullLogger())
	runner := NewRunner(host, registry, Source{Query: source, Packages: source}, t.TempDir(), slog.New(slog.DiscardHandler))
	t.Cleanup(runner.Close)
	ctx := context.Background()
	changes := make(chan struct{}, 4)
	runner.OnChange(func() { changes <- struct{}{} })
	changed := func() bool {
		select {
		case <-changes:
			return true
		case <-time.After(time.Second):
			return false
		}
	}

	registered := func() bool { _, ok := registry.Get("echo"); return ok }
	sync := func() {
		t.Helper()
		if err := runner.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}

	source.settings = controlplanerpc.ExtensionSettings{Extensions: []controlplanerpc.ExtensionSetting{{Name: "echo", Enabled: true, Settings: map[string]string{"k": "v"}}}}
	sync()
	if registered() {
		t.Fatal("an extension whose authority was never accepted must not run")
	}
	if len(changes) > 0 {
		t.Fatal("a sync that started nothing must not report a change")
	}

	source.pkg.AcceptedAuthority = &storepkg.Authority{}
	sync()
	got, err := registry.Resolve(secret.SecretRef{Engine: "echo", Key: "k"})
	if err != nil || got != "v" {
		t.Fatalf("accepted and enabled: Resolve = %q, %v; want v", got, err)
	}
	if !changed() {
		t.Fatal("starting an engine must report a change, so references resolve again")
	}

	source.settings.Extensions[0].Enabled = false
	sync()
	if registered() || host.Alive("echo") {
		t.Fatal("a disabled extension must be unregistered and its process stopped")
	}
	if !changed() {
		t.Fatal("stopping an engine must report a change")
	}
}
