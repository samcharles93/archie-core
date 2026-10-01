package kitrun

import (
	"errors"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

var errSecretNotConfigured = errors.New("secret not configured")

type fakeSecretResolver map[config.SecretRef]string

func (f fakeSecretResolver) Resolve(ref config.SecretRef) (string, error) {
	v, ok := f[ref]
	if !ok {
		return "", errSecretNotConfigured
	}
	return v, nil
}

func declaredCred(service string) spec.CredentialCapability {
	return spec.CredentialCapability{Service: service}
}

func declaredOAuth(service string) spec.CredentialCapability {
	c := declaredCred(service)
	c.OAuth = &spec.OAuth{TokenEndpoint: &spec.TokenEndpoint{Host: "auth.example.com"}}
	return c
}

// TestResolveCredentialsReadsTheLiveConfig is the "reads current, not a boot
// snapshot" requirement: two calls against the same Launcher, differing only
// in what l.Config now holds, must produce different results. A Launcher
// that captured Bindings once at construction (the reverted design) cannot
// pass this.
func TestResolveCredentialsReadsTheLiveConfig(t *testing.T) {
	ref := config.SecretRef{Engine: "env", Key: "OPENAI_KEY"}
	l := &Launcher{
		Config:  config.NewHolder(config.Config{}),
		Secrets: fakeSecretResolver{ref: "sk-live"},
	}
	req := Request{Org: "acme", GrantedServices: []string{"openai"}}
	creds := []spec.CredentialCapability{declaredCred("openai")}

	granted, kinds := l.resolveCredentials(req, creds)
	if len(granted) != 0 || len(kinds) != 0 {
		t.Fatalf("resolveCredentials with no configured binding = (%v, %v), want both empty", granted, kinds)
	}

	l.Config.Set(config.Config{Containers: config.ContainerConfig{
		Credentials: []config.CredentialBinding{{Service: "openai", Org: "acme", Secret: ref}},
	}})

	granted, kinds = l.resolveCredentials(req, creds)
	if granted["openai"] != "sk-live" || kinds["openai"] != egress.CredentialAPIKey {
		t.Fatalf("resolveCredentials after the binding was added = (%v, %v), want openai granted as an API key", granted, kinds)
	}
}

// TestResolveCredentialsIsAnIntersection is the isolation proof at the
// Launcher boundary: a secret the identity is not granted, or a binding from
// another org, is never in the resolved set, however the Kit declares it.
func TestResolveCredentialsIsAnIntersection(t *testing.T) {
	openaiRef := config.SecretRef{Engine: "env", Key: "OPENAI_KEY"}
	otherRef := config.SecretRef{Engine: "env", Key: "OTHER_KEY"}
	l := &Launcher{
		Config: config.NewHolder(config.Config{Containers: config.ContainerConfig{
			Credentials: []config.CredentialBinding{
				{Service: "openai", Org: "acme", Secret: openaiRef},
				{Service: "other-org-svc", Org: "other-org", Secret: otherRef},
				{Service: "claude-code", Org: "acme"},
			},
		}}),
		Secrets: fakeSecretResolver{openaiRef: "sk-acme", otherRef: "sk-other"},
	}

	tests := []struct {
		name    string
		req     Request
		creds   []spec.CredentialCapability
		want    map[string]string
		mutated bool
	}{
		{
			name:  "declared, granted, matching org resolves",
			req:   Request{Org: "acme", GrantedServices: []string{"openai"}},
			creds: []spec.CredentialCapability{declaredCred("openai")},
			want:  map[string]string{"openai": "sk-acme"},
		},
		{
			name:  "not granted never resolves even though declared and the org matches",
			req:   Request{Org: "acme", GrantedServices: nil},
			creds: []spec.CredentialCapability{declaredCred("openai")},
			want:  map[string]string{},
		},
		{
			name:  "a binding from another org never resolves even when granted",
			req:   Request{Org: "acme", GrantedServices: []string{"other-org-svc"}},
			creds: []spec.CredentialCapability{declaredCred("other-org-svc")},
			want:  map[string]string{},
		},
		{
			name:  "an OAuth service is granted without a config secret",
			req:   Request{Org: "acme", GrantedServices: []string{"claude-code"}},
			creds: []spec.CredentialCapability{declaredOAuth("claude-code")},
			want:  map[string]string{"claude-code": ""},
		},
		{
			name:  "an ungranted OAuth service never resolves",
			req:   Request{Org: "acme", GrantedServices: nil},
			creds: []spec.CredentialCapability{declaredOAuth("claude-code")},
			want:  map[string]string{},
		},
		{
			name:  "not declared by the Kit never resolves even though granted",
			req:   Request{Org: "acme", GrantedServices: []string{"openai"}},
			creds: nil,
			want:  map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			granted, _ := l.resolveCredentials(tt.req, tt.creds)
			if len(granted) != len(tt.want) {
				t.Fatalf("resolveCredentials() = %v, want %v", granted, tt.want)
			}
			for service, value := range tt.want {
				if got, ok := granted[service]; !ok || got != value {
					t.Fatalf("resolveCredentials()[%q] = %q, want %q", service, granted[service], value)
				}
			}
		})
	}
}

// TestResolveCredentialsTakesTheBoundKindFromTheBinding: the sandbox kit
// spec lets one credential@1 declare apiKey and oauth together, meaning
// "whichever the host has bound" (the reference docker/claude-code-kit and
// codex workloads both do, for anthropic and openai respectively). The org's
// binding -- not the Kit's declaration -- therefore decides which one a run
// carries, so a binding that names a secret must resolve that secret even
// when the Kit also declares OAuth, and a binding that names none must never
// resolve to an empty value the proxy would then inject.
func TestResolveCredentialsTakesTheBoundKindFromTheBinding(t *testing.T) {
	keyRef := config.SecretRef{Engine: "env", Key: "ANTHROPIC_API_KEY"}
	// The registry's documented zero-ref behaviour: resolves to "" with no
	// error, which is why "no value" cannot be told apart from "no secret"
	// by the resolved string alone.
	secrets := fakeSecretResolver{keyRef: "sk-ant-real", config.SecretRef{}: ""}
	// dual declares apiKey and oauth on one service, the spec's "whichever
	// the host has bound" shape.
	dual := func() spec.CredentialCapability {
		c := declaredOAuth("anthropic")
		c.APIKey = &spec.APIKey{Name: "ANTHROPIC_API_KEY", ProxyManaged: true}
		return c
	}

	tests := []struct {
		name      string
		bindings  []config.CredentialBinding
		creds     []spec.CredentialCapability
		service   string
		wantValue string
		wantKind  egress.CredentialKind
	}{
		{
			name:      "an API-key binding resolves its value for a Kit that also declares OAuth",
			bindings:  []config.CredentialBinding{{Service: "anthropic", Org: "acme", Secret: keyRef}},
			creds:     []spec.CredentialCapability{dual()},
			service:   "anthropic",
			wantValue: "sk-ant-real",
			wantKind:  egress.CredentialAPIKey,
		},
		{
			name:      "a binding with no secret of its own is the OAuth token set",
			bindings:  []config.CredentialBinding{{Service: "anthropic", Org: "acme"}},
			creds:     []spec.CredentialCapability{dual()},
			service:   "anthropic",
			wantValue: "",
			wantKind:  egress.CredentialOAuth,
		},
		{
			name:      "an OAuth-only Kit service with no binding secret is the OAuth token set",
			bindings:  []config.CredentialBinding{{Service: "oauth-only", Org: "acme"}},
			creds:     []spec.CredentialCapability{declaredOAuth("oauth-only")},
			service:   "oauth-only",
			wantValue: "",
			wantKind:  egress.CredentialOAuth,
		},
		{
			name:     "an API-key Kit service with no binding secret stays unbound, never empty",
			bindings: []config.CredentialBinding{{Service: "api-key-only", Org: "acme"}},
			creds:    []spec.CredentialCapability{declaredCred("api-key-only")},
			service:  "api-key-only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Launcher{
				Config: config.NewHolder(config.Config{Containers: config.ContainerConfig{
					Credentials: tt.bindings,
				}}),
				Secrets: secrets,
			}
			granted, kinds := l.resolveCredentials(Request{Org: "acme", GrantedServices: []string{tt.service}}, tt.creds)
			got, ok := granted[tt.service]
			if tt.wantKind == "" {
				if ok {
					t.Fatalf("resolveCredentials()[%q] = %q, want no credential at all", tt.service, got)
				}
				if kind := kinds[tt.service]; kind != "" {
					t.Fatalf("resolveCredentials() kind[%q] = %q, want unbound", tt.service, kind)
				}
				return
			}
			if !ok || got != tt.wantValue {
				t.Fatalf("resolveCredentials()[%q] = %q (present %v), want %q", tt.service, got, ok, tt.wantValue)
			}
			if kind := kinds[tt.service]; kind != tt.wantKind {
				t.Fatalf("resolveCredentials() kind[%q] = %q, want %q", tt.service, kind, tt.wantKind)
			}
		})
	}
}

// The skills store is mounted read-only whatever mode a Kit asks for, and
// not at all when the operator configured none.
func TestSkillsBinds(t *testing.T) {
	asks := []spec.AgentSkillsCapability{
		{Path: "/home/agent/.claude/skills", Mode: "readwrite"},
	}
	got := skillsBinds(asks, "/srv/shared")
	if len(got) != 1 || got[0] != "/srv/shared/.agents/skills:/home/agent/.claude/skills:ro" {
		t.Fatalf("skillsBinds = %v, want the store mounted read-only at the Kit's path", got)
	}
	if got := skillsBinds(asks, ""); len(got) != 0 {
		t.Fatalf("skillsBinds with no skills_dir = %v, want none", got)
	}
}
