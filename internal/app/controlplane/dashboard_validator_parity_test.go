package controlplane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The dashboard mirrors three of this package's validators by hand
// (ui/src/settings/validation.ts), and the model-role rule had already drifted:
// the dashboard required `^[^/\s]+\/\S+$` while the server only asks for a
// slash, so the dashboard refused values this package accepts. The fixture at
// testdata/dashboard-validators.json is the tie: this test runs the real server
// validators over it, and ui/test/control-plane-validator-parity.test.ts runs
// the real dashboard validators over the same cases. Changing a rule on one
// side alone fails one of the two tests (archie-core-ui-dashboard-3).
type dashboardValidatorFixture struct {
	Description string                              `json:"description"`
	Cases       map[string][]dashboardValidatorCase `json:"cases"`
}

type dashboardValidatorCase struct {
	Why   string          `json:"why"`
	Valid bool            `json:"valid"`
	Value json.RawMessage `json:"value"`
}

func TestDashboardValidatorsAgreeWithTheSharedFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "dashboard-validators.json"))
	if err != nil {
		t.Fatalf("read the shared validator fixture: %v", err)
	}
	var fixture dashboardValidatorFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode the shared validator fixture: %v", err)
	}
	if fixture.Description == "" {
		t.Error("the fixture has no description; it is the contract the dashboard mirror is read against")
	}

	// Each entry is the real entry point a Definition wires into Decode, keyed
	// to match the fixture (and the dashboard's own validator map).
	validators := map[string]func([]byte) error{
		ModelRoleAssignmentsKind: validateModelRoles,
		SchedulingPolicyKind:     validateScheduling,
		SchedulesKind:            func(input []byte) error { return validateAs(input, validateSchedules) },
	}

	for kind, validate := range validators {
		cases, ok := fixture.Cases[kind]
		if !ok {
			t.Errorf("fixture has no cases for %s, so nothing pins the dashboard mirror of it", kind)
			continue
		}
		var accepted, refused int
		for _, testCase := range cases {
			err := validate(testCase.Value)
			switch {
			case testCase.Valid && err != nil:
				t.Errorf("%s: fixture case %q is valid, but the server refused it: %v", kind, testCase.Why, err)
			case !testCase.Valid && err == nil:
				t.Errorf("%s: fixture case %q is invalid, but the server accepted it", kind, testCase.Why)
			case testCase.Valid:
				accepted++
			default:
				refused++
			}
		}
		if accepted == 0 || refused == 0 {
			t.Errorf("%s: the fixture must pin both an accepted and a refused case (accepted=%d refused=%d)", kind, accepted, refused)
		}
	}

	for kind := range fixture.Cases {
		if _, ok := validators[kind]; !ok {
			t.Errorf("fixture names %s, which this package has no validator for", kind)
		}
	}
}
