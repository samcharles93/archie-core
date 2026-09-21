package main

import "testing"

// TestLintRejectsSessionBoundWriting covers each rule's triggering shape and a
// nearby sentence it must not fire on, so a pattern tightened later cannot
// quietly stop catching the thing it exists for.
func TestLintRejectsSessionBoundWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		rule string
	}{
		{
			name: "line citation",
			doc:  "The pin happens in `internal/daemon/daemon.go:1485`.",
			rule: "line-citation",
		},
		{
			name: "markdown line citation",
			doc:  "The rule is stated in `event-sources-and-reactions.md:61-63`.",
			rule: "line-citation",
		},
		{
			name: "self-referential line range",
			doc:  "See this document's Dedup section, lines 155-159.",
			rule: "line-citation",
		},
		{
			name: "dated status heading",
			doc:  "**Status 2026-09-05: resolved** per the decision below.",
			rule: "dated-status",
		},
		{
			name: "dated verification",
			doc:  "Verified directly, 2026-09-03: the interface has no send method.",
			rule: "dated-status",
		},
		{
			name: "date before the verb",
			doc:  "`event-sources-and-reactions.md` (2026-08-22, decided) ruled that",
			rule: "dated-status",
		},
		{
			name: "dated survey",
			doc:  "### Candidate comparison (surveyed 2026-09-03, pkg.go.dev)",
			rule: "dated-status",
		},
		{
			name: "dated settlement",
			doc:  "### The two decisions (settled 2026-09-19, decided by position)",
			rule: "dated-status",
		},
		{
			name: "progress claim",
			doc:  "The coordinator is not yet wired into production task intake.",
			rule: "progress-claim",
		},
		{
			name: "readiness claim",
			doc:  "Forge is ready to implement once the ledger lands.",
			rule: "progress-claim",
		},
		{
			name: "phase progress",
			doc:  "Phase 2 is complete and Phase 3 starts next.",
			rule: "phase-progress",
		},
		{
			name: "temporal hedge",
			doc:  "`[notify]` today is one webhook URL, read in one place.",
			rule: "temporal-hedge",
		},
		{
			name: "session artefact",
			doc:  "Grounded by a crew investigation pass.",
			rule: "session-artefact",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := Lint("x.md", tc.doc)
			if len(findings) == 0 {
				t.Fatalf("Lint(%q) found nothing, want %s", tc.doc, tc.rule)
			}
			if findings[0].Rule != tc.rule {
				t.Fatalf("rule = %q, want %q", findings[0].Rule, tc.rule)
			}
		})
	}
}

// TestLintAcceptsDurableWriting: the rules target decay, not subject matter. A
// PRD may name a file, describe a phased plan, and carry its own status header.
func TestLintAcceptsDurableWriting(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"file without a line", "The pin happens in `internal/daemon/daemon.go`, in `Daemon.resolveWorkflowID`."},
		{"phases as design", "Phase 1 extracts the State Store. Phase 2 extracts the Gateway."},
		{"status header field", "**Status:** Draft"},
		{"date header field", "**Date:** 2026-09-03"},
		{"blocked-on header field", "**Blocked on:** `archie-core-t2db.24`"},
		{"a decision that names a date format", "Timestamps are stored as 2026-09-03 in the ledger."},
		{"bead id as a pointer", "Resolves `archie-core-t2db.19`; supersedes the earlier shape."},
		{"a dependency that exists is evidence", "`channels.Channel` already exists, so the position reuses it."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if findings := Lint("x.md", tc.doc); len(findings) != 0 {
				t.Fatalf("Lint(%q) = %v, want no findings", tc.doc, findings)
			}
		})
	}
}

// TestLintRequiresACanonicalStatus: RULES.md gives three statuses, and a
// free-text one is where "Implemented (wiring landed)" hid a progress report.
func TestLintRequiresACanonicalStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want bool
	}{
		{"draft", "# T\n\n**Status:** Draft\n", false},
		{"approved", "# T\n\n**Status:** Approved\n", false},
		{"finalised", "# T\n\n**Status:** Finalised\n", false},
		{"free text", "# T\n\n**Status:** Implemented (wiring landed)\n", true},
		{"status with a changelog", "# T\n\n**Status:** Ratified (rev. 2e). Rev. 2e adds a lookup\n", true},
		{"missing entirely", "# T\n\nBody.\n", true},
		{"a nested document's indented status", "# T\n\n**Status:** Draft\n\n1. Item\n\n   **Status:** Draft\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			for _, f := range LintDocument("x.md", tc.doc) {
				if f.Rule == "status" {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("status finding = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLintCatchesACitationSplitAcrossALineBreak: prose wraps, and a
// line-scoped rule saw "lines" and "155-159" as two innocent lines.
func TestLintCatchesACitationSplitAcrossALineBreak(t *testing.T) {
	doc := "never swallowed as a log line (this document's Dedup section, lines\n155-159, the drop-and-report rule)."
	var got bool
	for _, f := range Lint("x.md", doc) {
		if f.Rule == "line-citation" {
			got = true
		}
	}
	if !got {
		t.Fatal("no line-citation finding for a citation split across a line break")
	}
}

// TestLintSkipsFencedBlocks: a code listing may legitimately contain a line
// reference or a status string as the content being specified.
func TestLintSkipsFencedBlocks(t *testing.T) {
	doc := "Before.\n\n```\ninternal/daemon/daemon.go:1485\n```\n\nAfter."
	if findings := Lint("x.md", doc); len(findings) != 0 {
		t.Fatalf("Lint = %v, want no findings inside a fence", findings)
	}
}
