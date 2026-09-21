package main

import (
	"path/filepath"
	"strings"
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
// is the honest first report rather than a silent pass. The path is never
// created, so no file content is involved.
func TestLoadDeclarationsMissingFileIsEmpty(t *testing.T) {
	declarations, err := loadDeclarations(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("loadDeclarations(absent) error = %v, want nil", err)
	}
	if len(declarations.Surfaces) != 0 {
		t.Errorf("surfaces = %v, want empty", declarations.Surfaces)
	}
}

// TestParseDeclarations covers the decoding rules in memory, so no file is
// authored and no formatting of an external file can affect the outcome.
func TestParseDeclarations(t *testing.T) {
	t.Run("an empty object is usable", func(t *testing.T) {
		declarations, err := parseDeclarations(strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if declarations.Surfaces == nil {
			t.Error("surfaces = nil, want a non-nil empty map so a lookup cannot panic")
		}
	})

	t.Run("entries are decoded", func(t *testing.T) {
		declarations, err := parseDeclarations(strings.NewReader(
			`{"surfaces":{"s":{"Svc/Method":{"reason":"because","tracker":"abc.1"}}}}`,
		))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		entry := declarations.Surfaces["s"]["Svc/Method"]
		if entry.Reason != "because" || entry.Tracker != "abc.1" {
			t.Errorf("entry = %+v, want the reason and tracker preserved", entry)
		}
	})

	t.Run("malformed input is rejected", func(t *testing.T) {
		// A broken allowlist must fail loudly rather than degrading to "no
		// declarations", which would turn every declared entry into a finding
		// nobody could clear.
		if _, err := parseDeclarations(strings.NewReader("{not json")); err == nil {
			t.Error("error = nil, want a parse failure")
		}
	})
}

// TestStripJSComments pins the behaviour the extractor depends on: a commented
// call must not read as a live consumer, and a literal that merely looks like a
// comment must survive intact.
func TestStripJSComments(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"line comment goes", "a // gone\nb", "a \nb"},
		{"newline survives a line comment", "a // gone\nb", "a \nb"},
		{"block comment goes", "a/* gone */b", "ab"},
		{"unterminated block comment eats the tail", "a/* gone", "a"},
		{"comment marker inside a string survives", `f("// not a comment")`, `f("// not a comment")`},
		{"block marker inside a string survives", `f("/* kept */")`, `f("/* kept */")`},
		{"template literal survives", "f(`/* kept */`)", "f(`/* kept */`)"},
		{"escaped quote does not end the literal", `f("a\"// kept")`, `f("a\"// kept")`},
		{"unterminated literal keeps its tail", `f("abc`, `f("abc`},
		{"adjacent literals", `a("x")+b('y')`, `a("x")+b('y')`},
		{"code either side of a block comment", "a/*x*/b/*y*/c", "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripJSComments(tc.src); got != tc.want {
				t.Fatalf("stripJSComments(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
