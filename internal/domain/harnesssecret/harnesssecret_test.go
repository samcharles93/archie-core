package harnesssecret

import "testing"

func TestValidateRejectsIncomplete(t *testing.T) {
	cases := []Secret{
		{Service: "claude-code", AccessToken: "at"},
		{Org: "org-1", AccessToken: "at"},
		{Org: "org-1", Service: "claude-code"},
	}
	for _, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", s)
		}
	}
}

func TestValidateAcceptsComplete(t *testing.T) {
	s := Secret{Org: "org-1", Service: "claude-code", AccessToken: "at"}
	if err := s.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
