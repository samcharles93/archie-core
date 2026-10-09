package kit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/fetch"
	"github.com/docker/sandbox-kit-spec/v3/spec"
	"github.com/opencontainers/go-digest"

	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

func TestKitCapabilitySelection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		optional  bool
		refused   string
		wantError bool
	}{
		{"optional unknown group", true, "com.example/unavailable@1", false},
		{"required unknown group", false, "com.example/unavailable@1", true},
		{"optional forge group", true, spec.CapabilityCredential, false},
		{"required forge group", false, spec.CapabilityCredential, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rejected := spec.Capability{Type: tc.refused}
			if tc.refused == spec.CapabilityCredential {
				rejected.Config = map[string]any{"service": "github", "phase": "runtime", "apiKey": map[string]any{"name": "GH_TOKEN"}}
			}
			value := "resolved-value"
			descriptor := spec.Descriptor{SchemaVersion: spec.SchemaVersion, Kind: spec.KindWorkload, Args: map[string]spec.Arg{"setting": {Default: &value, Env: "SETTING"}}, Capabilities: []spec.Capability{
				{Type: spec.CapabilityAgentSessions, Config: map[string]any{"prompt": []string{"agent", "${{ kit.args.setting }}", "{{.Prompt}}"}}},
				{Type: spec.CapabilityAgentContext, Config: map[string]any{"filename": "AGENTS.md", "content": "retained guidance"}},
				{Group: &spec.CapabilityGroup{Optional: tc.optional, Capabilities: []spec.Capability{
					rejected,
					{Type: spec.CapabilityLifecycle, Config: map[string]any{"startup": []any{map[string]any{"command": "touch /must-not-run"}}, "files": []any{map[string]any{"path": "/must-not-write", "content": "rejected"}}}},
					{Type: spec.CapabilityAgentContext, Config: map[string]any{"content": "must not appear"}},
				}}},
			}}
			client, ref := kitRegistry(t, descriptor)
			resolved, err := client.Resolve(t.Context(), []fetch.Request{{Reference: ref}}, fetch.WithCapabilitySelector(SelectCapability))
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "required capability selection rejected") {
					t.Fatalf("required group error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			plan, err := FromResolved(resolved)
			if err != nil {
				t.Fatal(err)
			}
			launch, err := Assemble(plan, ImageConfig{}, LaunchParams{})
			if err != nil {
				t.Fatal(err)
			}
			if len(launch.Startup) != 0 || len(launch.Files) != 0 {
				t.Fatalf("rejected group effects survived: %+v", launch)
			}
			if len(plan.ContextSources) != 1 || plan.ContextSources[0].Content != "retained guidance" {
				t.Fatalf("context sources=%+v", plan.ContextSources)
			}
			if resolved.ContainerEnv["SETTING"] != value || plan.Sessions.Prompt[1] != value {
				t.Fatalf("argument exports or prompt not resolved: %+v", resolved)
			}
			if len(plan.Skipped) != 1 {
				t.Fatalf("skipped=%+v", plan.Skipped)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := client.Resolve(ctx, []fetch.Request{{Reference: ref}}, fetch.WithCapabilitySelector(SelectCapability)); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled resolve=%v", err)
			}
		})
	}
}

func kitRegistry(t *testing.T, descriptor spec.Descriptor) (*fetch.Client, string) {
	t.Helper()
	raw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config":      map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": digest.FromString("config"), "size": 2},
		"layers":      []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": digest.FromString("layer"), "size": 1}},
		"annotations": map[string]string{spec.AnnotationDescriptor: string(raw)},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/manifests/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(manifest).String())
		w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(manifest)
		}
	}))
	t.Cleanup(server.Close)
	client, err := fetch.New()
	if err != nil {
		t.Fatal(err)
	}
	return client, strings.TrimPrefix(server.URL, "http://") + "/kit:1.0.0"
}

func TestKitContextDiscovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, directory, want string }{
		{"legacy", "", "/archie/AGENTS.md"},
		{"explicit", "/home/agent/.codex", "/home/agent/.codex/AGENTS.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor := spec.Descriptor{SchemaVersion: spec.SchemaVersion, Kind: spec.KindWorkload, Capabilities: []spec.Capability{
				{Type: spec.CapabilityAgentSessions, Config: map[string]any{"prompt": []string{"agent", "{{.Prompt}}"}}},
				{Type: spec.CapabilityAgentContext, Config: map[string]any{"filename": "AGENTS.md", "directory": tc.directory, "content": "guidance"}},
			}}
			if tc.directory == "" {
				delete(descriptor.Capabilities[1].Config, "directory")
			}
			client, ref := kitRegistry(t, descriptor)
			resolved, err := client.Resolve(t.Context(), []fetch.Request{{Reference: ref}}, fetch.WithCapabilitySelector(SelectCapability))
			if err != nil {
				t.Fatal(err)
			}
			plan, err := FromResolved(resolved)
			if err != nil {
				t.Fatal(err)
			}
			files, err := contextFiles(plan)
			if err != nil {
				t.Fatal(err)
			}
			if got := files[len(files)-1].Path; got != tc.want {
				t.Fatalf("discovery path=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestKitCredentialExposureByPhase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		phases           spec.Phases
		install, runtime bool
	}{
		{"install", spec.Phases{"install"}, true, false},
		{"runtime", spec.Phases{"runtime"}, false, true},
		{"both", spec.Phases{"install", "runtime"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := spec.Credential{Service: "provider", Phase: tc.phases, APIKey: &spec.APIKey{Name: "PROVIDER_KEY"}}
			entry, err := spec.CapabilityWithConfig(spec.Capability{Type: spec.CapabilityCredential}, credential)
			if err != nil {
				t.Fatal(err)
			}
			plan := &Plan{Capabilities: []spec.Capability{*entry, {Type: spec.CapabilityLifecycle, Config: map[string]any{
				"install": []any{map[string]any{"command": "true", "env": []string{"PROVIDER_KEY"}}},
				"startup": []any{map[string]any{"command": "true", "env": []string{"PROVIDER_KEY"}}},
			}}}}
			launch, err := Assemble(plan, ImageConfig{}, LaunchParams{})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				name string
				env  []string
				want bool
			}{
				{"install", launch.Install[0].Env, tc.install}, {"startup", launch.Startup[0].Env, tc.runtime}, {"harness", launch.Harness.Env, tc.runtime},
			} {
				if got := slices.Contains(check.env, "PROVIDER_KEY="+egress.Sentinel); got != check.want {
					t.Errorf("%s sentinel exposed=%v, want %v", check.name, got, check.want)
				}
			}
			credential.APIKey = nil
			credential.OAuth = &spec.OAuth{Sentinels: &spec.Sentinels{AccessToken: "access-sentinel"}, CredentialFile: &spec.CredentialFile{Path: "/tmp/provider.json", Structure: map[string]any{"token": "{{.AccessToken}}"}}}
			entry, err = spec.CapabilityWithConfig(spec.Capability{Type: spec.CapabilityCredential}, credential)
			if err != nil {
				t.Fatal(err)
			}
			plan.Capabilities[0] = *entry
			launch, err = Assemble(plan, ImageConfig{}, LaunchParams{Bound: map[string]egress.CredentialKind{"provider": egress.CredentialOAuth}})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(launch.InstallFiles) > 0; got != tc.install {
				t.Errorf("install file present=%v, want %v", got, tc.install)
			}
			if got := len(launch.Files) > 0; got != tc.runtime {
				t.Errorf("runtime file present=%v, want %v", got, tc.runtime)
			}
		})
	}
}
