package stableid

import "testing"

func TestValid(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"tdd", true},
		{"forge.github", true},
		{"step-type2.v1", true},
		{"", false},
		{"Tdd", false},
		{"2fa", false},
		{"a..b", false},
		{"a.", false},
		{"-a", false},
		{"a b", false},
		{"a_b", false},
	}
	for _, tt := range tests {
		if got := Valid(tt.id); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
