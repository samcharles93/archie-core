package binding

import "testing"

func validBinding() Binding {
	return Binding{
		Name:      "sentry alerts",
		Matcher:   Matcher{Source: "sentry"},
		MappingID: "m1",
		Workflow:  "implement",
	}
}

// TestValidateAcceptsNoRepoPin pins backward compatibility: a binding
// with neither Owner nor Repo set (today's only shape) must remain
// valid -- resolveBindingRepo's single-configured-repo fallback still
// applies to it.
func TestValidateAcceptsNoRepoPin(t *testing.T) {
	b := validBinding()
	if err := b.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// TestValidateAcceptsBothOwnerAndRepo is the multi-repo fix: a binding
// naming a specific owner/repo must be accepted.
func TestValidateAcceptsBothOwnerAndRepo(t *testing.T) {
	b := validBinding()
	b.Owner, b.Repo = "acme", "widget"
	if err := b.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsPartialRepoPin(t *testing.T) {
	tests := []struct {
		name        string
		owner, repo string
	}{
		{"owner only", "acme", ""},
		{"repo only", "", "widget"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := validBinding()
			b.Owner, b.Repo = tt.owner, tt.repo
			if err := b.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want error for partial owner/repo pin (owner=%q repo=%q)", tt.owner, tt.repo)
			}
		})
	}
}

func TestMatcherMatchesOnlyDispatchableEventsFromItsSource(t *testing.T) {
	m := Matcher{Source: "sentry"}
	tests := []struct {
		source       string
		dispatchable bool
		want         bool
	}{
		{"sentry", true, true},
		{"sentry", false, false},
		{"other", true, false},
	}
	for _, tt := range tests {
		if got := m.Matches(tt.source, tt.dispatchable); got != tt.want {
			t.Errorf("Matches(%q, %v) = %v, want %v", tt.source, tt.dispatchable, got, tt.want)
		}
	}
}
