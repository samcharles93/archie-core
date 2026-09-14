package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClassify covers the three-state rule plus the staleness rule that runs
// in the opposite direction. The staleness cases are the important ones: an
// allowlist that permits something which is no longer true is how a gate
// quietly stops checking anything.
func TestClassify(t *testing.T) {
	subjects := []Subject{
		{Name: "Svc/Consumed", Evidence: "proto:x", Consumed: true, Consumer: "internal/infrastructure/staterpc/client.go:41"},
		{Name: "Svc/Declared", Evidence: "proto:y", Consumed: false},
		{Name: "Svc/Bare", Evidence: "proto:z", Consumed: false},
		{Name: "Svc/FreshlyConsumed", Evidence: "proto:w", Consumed: true, Consumer: "internal/foo/bar.go:9"},
	}
	declarations := Declarations{Surfaces: map[string]map[string]Declaration{
		"proto/state/v1": {
			"Svc/Declared":        {Reason: "no consumer yet; extraction is planned", Tracker: "archie-core-8cda.4"},
			"Svc/FreshlyConsumed": {Reason: "was unconsumed at audit time", Tracker: "archie-core-8cda.6"},
			"Svc/Gone":            {Reason: "surface removed it", Tracker: "archie-core-8cda.7"},
		},
	}}

	findings := classify("proto/state/v1", subjects, declarations)
	got := map[string]Class{}
	for _, finding := range findings {
		got[finding.Subject] = finding.Class
	}

	want := map[string]Class{
		"Svc/Consumed":        Consumed,
		"Svc/Declared":        DeclaredUnconsumed,
		"Svc/Bare":            Undeclared,
		"Svc/FreshlyConsumed": Stale,
		"Svc/Gone":            Stale,
	}
	for subject, class := range want {
		if got[subject] != class {
			t.Errorf("classify(%q) = %q, want %q", subject, got[subject], class)
		}
	}
	if len(findings) != len(want) {
		t.Errorf("classify produced %d findings, want %d", len(findings), len(want))
	}
}

// TestClassifyDeclaredEntryKeepsProvenance asserts the reason and tracker
// survive classification, because the declaration file is the only place the
// debt is legible.
func TestClassifyDeclaredEntryKeepsProvenance(t *testing.T) {
	subjects := []Subject{{Name: "Svc/Declared"}}
	declarations := Declarations{Surfaces: map[string]map[string]Declaration{
		"s": {"Svc/Declared": {Reason: "the consequence, stated", Tracker: "archie-core-abc.1"}},
	}}
	findings := classify("s", subjects, declarations)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Evidence != "the consequence, stated" {
		t.Errorf("evidence = %q, want the reason", findings[0].Evidence)
	}
	if findings[0].Tracker != "archie-core-abc.1" {
		t.Errorf("tracker = %q, want the bead id", findings[0].Tracker)
	}
}

// TestUndeclaredCountCountsOnlyFindings asserts a healthy surface contributes
// zero, so a strict run over a fully-declared surface passes.
func TestUndeclaredCountCountsOnlyFindings(t *testing.T) {
	findings := []Finding{
		{Subject: "a", Class: Consumed},
		{Subject: "b", Class: DeclaredUnconsumed},
		{Subject: "c", Class: Undeclared},
		{Subject: "d", Class: Stale},
	}
	if got := UndeclaredCount(findings); got != 2 {
		t.Errorf("UndeclaredCount = %d, want 2 (undeclared + stale)", got)
	}
}

// TestLoadDeclarationsMissingFileIsEmpty asserts an absent allowlist is the
// correct starting state: every unconsumed subject becomes undeclared, which
// is the honest first report rather than a silent pass.
func TestLoadDeclarationsMissingFileIsEmpty(t *testing.T) {
	declarations, err := loadDeclarations(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("loadDeclarations(absent) error = %v, want nil", err)
	}
	if len(declarations.Surfaces) != 0 {
		t.Errorf("surfaces = %v, want empty", declarations.Surfaces)
	}
}

// TestLoadDeclarationsRejectsMalformed asserts a broken allowlist fails loudly
// rather than degrading to "no declarations", which would silently turn every
// declared entry into a finding nobody could clear.
func TestLoadDeclarationsRejectsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDeclarations(path); err == nil {
		t.Error("loadDeclarations(malformed) error = nil, want a parse failure")
	}
}
