package storepkg

import "testing"

// An update applies unattended only when the operator's accepted record
// already covers everything it declares.
func TestInstalledWidens(t *testing.T) {
	accepted := &Authority{ForgePermissions: []string{"pull_requests:read"}, EgressHosts: []string{"api.example.com"}}
	tests := []struct {
		name     string
		accepted *Authority
		next     Authority
		want     bool
	}{
		{name: "equal authority does not widen", accepted: accepted, next: *accepted},
		{name: "narrower authority does not widen", accepted: accepted, next: Authority{EgressHosts: []string{"api.example.com"}}},
		{name: "a new egress host widens", accepted: accepted, next: Authority{ForgePermissions: []string{"pull_requests:read"}, EgressHosts: []string{"api.example.com", "evil.example.com"}}, want: true},
		{name: "a grant moved across fields widens", accepted: accepted, next: Authority{Tools: []string{"api.example.com"}}, want: true},
		{name: "nothing accepted, nothing declared", next: Authority{}},
		{name: "nothing accepted, anything declared widens", next: Authority{Env: []string{"TOKEN"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Installed{AcceptedAuthority: tt.accepted}).Widens(tt.next); got != tt.want {
				t.Fatalf("Widens = %v; want %v", got, tt.want)
			}
		})
	}
}
