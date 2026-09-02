package binding

import "testing"

func validBinding() Binding {
	return Binding{
		Name:      "sentry alerts",
		Matcher:   Matcher{Source: "sentry"},
		MappingID: 1,
		Workflow:  "implement",
		Secret:    "0123456789abcdef0123456789abcdef",
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

// TestValidateRejectsPartialRepoPin: Owner and Repo are a pair. A
// binding naming only one of them is ambiguous -- it isn't "no pin"
// (that's both empty) and it isn't a complete pin, so it must be
// rejected at validation time rather than silently guessing which
// half was meant.
// TestValidateForUpdateAcceptsEmptySecret is the fix for a real bug found
// via browser testing (t2db.8): UpdateBinding's own documented contract
// treats an empty Secret as "preserve the existing one" (COALESCE against
// NULLIF), but the API handler validated with the create-time Validate,
// which rejected any edit that didn't retype the full secret -- making
// the store's own documented behavior unreachable.
func TestValidateForUpdateAcceptsEmptySecret(t *testing.T) {
	b := validBinding()
	b.Secret = ""
	if err := b.ValidateForUpdate(); err != nil {
		t.Fatalf("ValidateForUpdate() = %v, want nil for an empty (preserve-existing) secret", err)
	}
}

// TestValidateForUpdateStillEnforcesLengthOnANewSecret confirms the fix
// isn't a blanket bypass: a genuinely-supplied short secret on update is
// still rejected, same floor as create.
func TestValidateForUpdateStillEnforcesLengthOnANewSecret(t *testing.T) {
	b := validBinding()
	b.Secret = "short"
	if err := b.ValidateForUpdate(); err == nil {
		t.Fatal("ValidateForUpdate() = nil, want error for a too-short (but non-empty) secret")
	}
}

// TestValidateRejectsEmptySecretOnCreate pins that the fix is scoped to
// update only -- Validate (used at create time, where there is no
// existing secret to fall back to) still requires a real one.
func TestValidateRejectsEmptySecretOnCreate(t *testing.T) {
	b := validBinding()
	b.Secret = ""
	if err := b.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for an empty secret at create time")
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
