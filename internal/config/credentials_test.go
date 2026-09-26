package config

import (
	"strings"
	"testing"
)

// TestBoundCredentialsIsAnIntersection is the mutation-checked isolation
// proof docs/prds/external-agent-harness.md and orgs-and-access.md require:
// declared (what a Kit's descriptor asks for) and granted (an identity's
// GrantedCredentials) are independent facts, and a binding's Org is a third.
// Flip any single one of the three and the service must fall out of the
// result -- never partially, never because another passed.
func TestBoundCredentialsIsAnIntersection(t *testing.T) {
	c := ContainerConfig{Credentials: []CredentialBinding{
		{Service: "openai", Org: "acme", Secret: SecretRef{Engine: "env", Key: "OPENAI_KEY"}},
		{Service: "other-org-svc", Org: "other", Secret: SecretRef{Engine: "env", Key: "X"}},
	}}

	tests := []struct {
		name     string
		org      string
		granted  []string
		declared []string
		want     []string
	}{
		{
			name: "org, granted and declared all agree", org: "acme",
			granted: []string{"openai"}, declared: []string{"openai"},
			want: []string{"openai"},
		},
		{
			name: "declared but not granted resolves nothing", org: "acme",
			granted: nil, declared: []string{"openai"},
			want: nil,
		},
		{
			name: "granted but not declared resolves nothing", org: "acme",
			granted: []string{"openai"}, declared: nil,
			want: nil,
		},
		{
			name: "granted and declared but the wrong org resolves nothing", org: "not-acme",
			granted: []string{"openai"}, declared: []string{"openai"},
			want: nil,
		},
		{
			name: "a service bound to a different org is never returned regardless of grants", org: "acme",
			granted: []string{"other-org-svc"}, declared: []string{"other-org-svc"},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.BoundCredentials(tt.org, tt.granted, tt.declared)
			var names []string
			for name := range got {
				names = append(names, name)
			}
			if len(names) != len(tt.want) {
				t.Fatalf("BoundCredentials() = %v, want %v", names, tt.want)
			}
			for _, want := range tt.want {
				if _, ok := got[want]; !ok {
					t.Fatalf("BoundCredentials() = %v, want it to carry %q", names, want)
				}
			}
		})
	}
}

func TestValidateCredentialBindings(t *testing.T) {
	tests := []struct {
		name    string
		c       ContainerConfig
		wantErr string
	}{
		{name: "no bindings", c: ContainerConfig{}},
		{name: "one binding", c: ContainerConfig{Credentials: []CredentialBinding{{Service: "openai", Org: "acme"}}}},
		{
			name:    "empty service name",
			c:       ContainerConfig{Credentials: []CredentialBinding{{Service: "", Org: "acme"}}},
			wantErr: "service name must not be empty",
		},
		{
			name: "duplicate service in the same org",
			c: ContainerConfig{Credentials: []CredentialBinding{
				{Service: "openai", Org: "acme"},
				{Service: "openai", Org: "acme"},
			}},
			wantErr: "bound more than once",
		},
		{
			name: "same service, different orgs is not a duplicate",
			c: ContainerConfig{Credentials: []CredentialBinding{
				{Service: "openai", Org: "acme"},
				{Service: "openai", Org: "other"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.c.ValidateCredentialBindings()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateCredentialBindings() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateCredentialBindings() = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
