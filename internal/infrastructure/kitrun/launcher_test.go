package kitrun

import (
	"errors"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/config"
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

	granted, bound := l.resolveCredentials(req, creds)
	if len(granted) != 0 || len(bound) != 0 {
		t.Fatalf("resolveCredentials with no configured binding = (%v, %v), want both empty", granted, bound)
	}

	l.Config.Set(config.Config{Containers: config.ContainerConfig{
		Credentials: []config.CredentialBinding{{Service: "openai", Org: "acme", Secret: ref}},
	}})

	granted, bound = l.resolveCredentials(req, creds)
	if granted["openai"] != "sk-live" || len(bound) != 1 || bound[0] != "openai" {
		t.Fatalf("resolveCredentials after the binding was added = (%v, %v), want openai granted", granted, bound)
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
				if granted[service] != value {
					t.Fatalf("resolveCredentials()[%q] = %q, want %q", service, granted[service], value)
				}
			}
		})
	}
}
