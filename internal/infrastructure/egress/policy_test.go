package egress

import (
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

func TestRulesAllows(t *testing.T) {
	rules := compileRules(&spec.NetworkRules{
		Allow: []string{"api.example.com:443", "downloads.example.com", "*.cdn.example.net", "10.0.0.5:8080"},
		Deny:  []string{"blocked.cdn.example.net"},
	})
	tests := []struct {
		host string
		port int
		want bool
	}{
		{"api.example.com", 443, true},
		{"api.example.com", 80, false},
		{"API.Example.COM.", 443, true},
		{"downloads.example.com", 443, true},
		{"downloads.example.com", 8443, true},
		{"a.cdn.example.net", 443, true},
		{"cdn.example.net", 443, false},
		{"a.b.cdn.example.net", 443, false},
		{"blocked.cdn.example.net", 443, false},
		{"10.0.0.5", 8080, true},
		{"10.0.0.5", 80, false},
		{"example.com", 443, false},
		{"evil-api.example.com", 443, false},
		{"api.example.com.evil.test", 443, false},
	}
	for _, tt := range tests {
		if got := rules.allows(tt.host, tt.port); got != tt.want {
			t.Errorf("allows(%q, %d) = %v, want %v", tt.host, tt.port, got, tt.want)
		}
	}
}

func TestRulesEverything(t *testing.T) {
	for _, all := range []string{"*", "**"} {
		rules := compileRules(&spec.NetworkRules{Allow: []string{all}, Deny: []string{"telemetry.example.com"}})
		if !rules.allows("anything.example.org", 443) {
			t.Errorf("%q does not allow an arbitrary host", all)
		}
		if rules.allows("telemetry.example.com", 443) {
			t.Errorf("deny does not win over %q", all)
		}
	}
}

func TestNilRulesAllowNothing(t *testing.T) {
	if compileRules(nil).allows("api.example.com", 443) {
		t.Fatal("a phase with no rules allowed egress")
	}
}
