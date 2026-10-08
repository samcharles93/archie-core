package kitrun

import (
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

// A setup session has no task and no identity grant, so it must bind only the
// org's own bindings for the Kit's declared services. Widening that
// intersection would let one org's terminal carry another org's credential,
// or a service the Kit never declared.
func TestOrgCredentialKinds(t *testing.T) {
	t.Parallel()

	bindings := []config.CredentialBinding{
		{Service: "github", Org: "acme", Secret: config.SecretRef{Engine: "builtin", Key: "gh"}},
		{Service: "linear", Org: "acme"},
		{Service: "github", Org: "other", Secret: config.SecretRef{Engine: "builtin", Key: "gh"}},
	}
	creds := []spec.CredentialCapability{{Service: "github"}, {Service: "linear"}, {Service: "unbound"}}

	cases := []struct {
		name     string
		org      string
		want     map[string]egress.CredentialKind
		services []string
	}{
		{
			name: "binds the org's own declared services",
			org:  "acme",
			want: map[string]egress.CredentialKind{
				"github": egress.CredentialAPIKey,
				"linear": egress.CredentialOAuth,
			},
			services: []string{"github", "linear"},
		},
		{
			name: "another org's binding never resolves",
			org:  "other",
			want: map[string]egress.CredentialKind{"github": egress.CredentialAPIKey},
			services: []string{
				"github",
			},
		},
		{
			name:     "unknown org binds nothing",
			org:      "nobody",
			want:     map[string]egress.CredentialKind{},
			services: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, services := orgCredentialKinds(bindings, tc.org, creds)
			if len(got) != len(tc.want) {
				t.Fatalf("kinds = %v, want %v", got, tc.want)
			}
			for service, kind := range tc.want {
				if got[service] != kind {
					t.Errorf("kind[%s] = %q, want %q", service, got[service], kind)
				}
			}
			if len(services) != len(tc.services) {
				t.Fatalf("services = %v, want %v", services, tc.services)
			}
		})
	}
}
