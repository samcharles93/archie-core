package task

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

func TestWorkflowEnablementEnabled(t *testing.T) {
	enablement := WorkflowEnablement{Orgs: map[org.OrgID]OrgWorkflows{
		"acme": {Disabled: []string{"triage"}},
	}}
	cases := []struct {
		org      org.OrgID
		workflow string
		want     bool
	}{
		{"acme", "triage", false},
		{"acme", "implement", true},
		{"other", "triage", true},
	}
	for _, c := range cases {
		if got := enablement.Enabled(c.org, c.workflow); got != c.want {
			t.Errorf("Enabled(%q, %q) = %v, want %v", c.org, c.workflow, got, c.want)
		}
	}
}

func TestWorkflowEnablementSetEnabledRoundTrips(t *testing.T) {
	var enablement WorkflowEnablement
	enablement = enablement.SetEnabled("acme", "triage", false)
	enablement = enablement.SetEnabled("acme", "triage", false)
	if enablement.Enabled("acme", "triage") || len(enablement.Orgs["acme"].Disabled) != 1 {
		t.Fatalf("disable twice: %+v", enablement)
	}
	enablement = enablement.SetEnabled("acme", "triage", true)
	if !enablement.Enabled("acme", "triage") {
		t.Fatalf("re-enable: %+v", enablement)
	}
}

func TestWorkflowEnablementValidate(t *testing.T) {
	cases := map[string]struct {
		enablement WorkflowEnablement
		ok         bool
	}{
		"empty":           {WorkflowEnablement{}, true},
		"valid":           {WorkflowEnablement{Orgs: map[org.OrgID]OrgWorkflows{"acme": {Disabled: []string{"triage"}}}}, true},
		"blank org":       {WorkflowEnablement{Orgs: map[org.OrgID]OrgWorkflows{" ": {Disabled: []string{"triage"}}}}, false},
		"blank workflow":  {WorkflowEnablement{Orgs: map[org.OrgID]OrgWorkflows{"acme": {Disabled: []string{""}}}}, false},
		"duplicate entry": {WorkflowEnablement{Orgs: map[org.OrgID]OrgWorkflows{"acme": {Disabled: []string{"triage", "triage"}}}}, false},
	}
	for name, c := range cases {
		if err := c.enablement.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", name, err, c.ok)
		}
	}
}
