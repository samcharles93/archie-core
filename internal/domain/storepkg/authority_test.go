package storepkg

import "testing"

func TestAuthorityCovers(t *testing.T) {
	tests := []struct {
		name   string
		have   Authority
		want   Authority
		covers bool
	}{
		{
			name:   "equal authority covers itself",
			have:   Authority{ForgePermissions: []string{"read", "comment"}, Tools: []string{"shell"}},
			want:   Authority{ForgePermissions: []string{"comment", "read"}, Tools: []string{"shell"}},
			covers: true,
		},
		{
			name:   "wider accepted authority covers the narrower request",
			have:   Authority{ForgePermissions: []string{"read", "comment", "review"}},
			want:   Authority{ForgePermissions: []string{"read"}},
			covers: true,
		},
		{
			name:   "an update asking for a forge permission the record lacks is wider",
			have:   Authority{ForgePermissions: []string{"read"}},
			want:   Authority{ForgePermissions: []string{"read", "push"}},
			covers: false,
		},
		{
			name:   "grants are never interchangeable across fields",
			have:   Authority{Tools: []string{"registry"}},
			want:   Authority{CredentialServices: []string{"registry"}},
			covers: false,
		},
		{
			name: "coverage is checked along every field",
			have: Authority{
				CredentialServices: []string{"github"},
				EgressHosts:        []string{"api.github.com"},
				ForgePermissions:   []string{"read"},
				Triggers:           []string{"repo/archie-core:pull_request"},
				Tools:              []string{"shell"},
			},
			want: Authority{
				CredentialServices: []string{"github"},
				EgressHosts:        []string{"internal.invalid"},
			},
			covers: false,
		},
		{
			name:   "nothing accepted covers nothing requested",
			have:   Authority{},
			want:   Authority{},
			covers: true,
		},
		{
			name:   "an empty accepted record covers any request at all",
			have:   Authority{},
			want:   Authority{Tools: []string{"shell"}},
			covers: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.have.Covers(tt.want); got != tt.covers {
				t.Fatalf("Covers = %v, want %v", got, tt.covers)
			}
		})
	}
}

func TestAuthorityValidate(t *testing.T) {
	tests := []struct {
		name      string
		authority Authority
		wantErr   string
	}{
		{name: "valid authority", authority: Authority{ForgePermissions: []string{"read"}, Tools: []string{"shell"}}},
		{name: "invalid forge permission", authority: Authority{ForgePermissions: []string{"admin"}}, wantErr: `invalid forge permission "admin"`},
		{name: "blank credential service", authority: Authority{CredentialServices: []string{" "}}, wantErr: "credential service 0: name is required"},
		{name: "blank egress host", authority: Authority{EgressHosts: []string{""}}, wantErr: "egress host 0: name is required"},
		{name: "blank trigger", authority: Authority{Triggers: []string{""}}, wantErr: "trigger 0: name is required"},
		{name: "blank tool", authority: Authority{Tools: []string{""}}, wantErr: "tool 0: name is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.authority.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Validate = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
