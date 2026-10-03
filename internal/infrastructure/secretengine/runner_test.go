package secretengine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

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

func (e *echoEngine) Resolve(_ context.Context, r *secretenginev1.ResolveRequest) (*secretenginev1.ResolveResponse, error) {
	if name, ok := strings.CutPrefix(r.GetKey(), "ENV:"); ok {
		return &secretenginev1.ResolveResponse{Value: os.Getenv(name)}, nil
	}
	if v, ok := e.settings[r.GetKey()]; ok {
		return &secretenginev1.ResolveResponse{Value: v}, nil
	}
	return nil, status.Error(codes.NotFound, "no such key")
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

func TestHostLaunch(t *testing.T) {
	t.Setenv("ARCHIE_TEST_SECRET", "host-only")
	t.Setenv("ARCHIE_TEST_PASSED", "granted")
	good := selfDigest(t)

	tests := []struct {
		name    string
		sha     string
		key     string
		want    string
		wantErr string
	}{
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
			spec := extension.Spec{Name: "echo", Path: os.Args[0], SHA256: tt.sha, Env: []string{"ARCHIE_TEST_PASSED"}}
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

func (f *fakeSource) ListInstalled(context.Context, string) ([]storepkg.Installed, error) {
	listed := f.pkg
	listed.Layer = nil
	return []storepkg.Installed{listed}, nil
}

func (f *fakeSource) GetInstalled(context.Context, string, string) (storepkg.Installed, error) {
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

	source.pkg.AcceptedAuthority = &storepkg.Authority{}
	sync()
	got, err := registry.Resolve(secret.SecretRef{Engine: "echo", Key: "k"})
	if err != nil || got != "v" {
		t.Fatalf("accepted and enabled: Resolve = %q, %v; want v", got, err)
	}

	source.settings.Extensions[0].Enabled = false
	sync()
	if registered() || host.Alive("echo") {
		t.Fatal("a disabled extension must be unregistered and its process stopped")
	}
}
