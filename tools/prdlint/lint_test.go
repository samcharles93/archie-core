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

// TestLintSkipsFencedBlocks: a code listing may legitimately contain a line
// reference or a status string as the content being specified.
func TestLintSkipsFencedBlocks(t *testing.T) {
	doc := "Before.\n\n```\ninternal/daemon/daemon.go:1485\n```\n\nAfter."
	if findings := Lint("x.md", doc); len(findings) != 0 {
		t.Fatalf("Lint = %v, want no findings inside a fence", findings)
	}
}
