package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Finding is one rule violation, addressed the way an editor reads it.
type Finding struct {
	File string
	Line int
	Rule string
	Text string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Rule, strings.TrimSpace(f.Text))
}

// rule is one forbidden shape. Each rejects writing whose truth expires while
// the sentence around it still reads as current.
type rule struct {
	name    string
	pattern *regexp.Regexp
	remedy  string
}

// rules is the closed set. Each pattern targets a shape that decays, never a
// topic: a PRD may discuss phases, dates and shipped work, and says so without
// asserting the state of the tree on the day it was written.
var rules = []rule{
	{
		name:    "line-citation",
		pattern: regexp.MustCompile(`\.(go|md|ya?ml|toml|proto|ts|js|sql):\d+|\blines? \d+(-\d+)?\b`),
		remedy:  "cite the file and the symbol, not the line. Line numbers move on the next edit above them and the stale citation still reads as authoritative. This covers a document citing its own line numbers.",
	},
	{
		name:    "dated-status",
		pattern: regexp.MustCompile(`(?i)\b(status|resolved|settled|decided|agreed|approved|supersedes|superseded|withdrawn|shipped|landed|complete|completed|done|wired|verified|corrected|confirmed)\b[^.\n]{0,30}\b20\d\d-\d\d-\d\d\b`),
		remedy:  "drop the date and the state. A PRD says what the design is; the tracker and git history say when it happened.",
	},
	{
		name:    "progress-claim",
		pattern: regexp.MustCompile(`(?i)\b(not yet|already|now|currently|today)\s+(wired|built|implemented|shipped|landed|complete)\b|\bis (now|already) \w+ed\b|\bready to (implement|design|build)\b|\bas of (today|this writing)\b`),
		remedy:  "state the design, not its build state. \"X is not yet wired\" is true for one week and misleading afterwards. Naming a dependency that exists is evidence and stays; reporting progress on the work this document designs does not.",
	},
	{
		name:    "phase-progress",
		pattern: regexp.MustCompile(`(?i)\bphases?\s+\d[^.\n]{0,40}\b(complete|completed|done|shipped|landed|in progress|underway|remaining)\b|\b(complete|completed|done|shipped|landed|in progress)\b[^.\n]{0,20}\bphases?\s+\d`),
		remedy:  "a phased plan is durable design; which phase is finished is not. Name the phases, not their state.",
	},
	{
		name:    "temporal-hedge",
		pattern: regexp.MustCompile(`(?i)\b(today|currently|right now|at present|at the moment|as things stand|for now)\b`),
		remedy:  "\"today\" dates the sentence to the day it was written, so the claim it qualifies is false the moment the design lands. Describe the shape being replaced without asserting it is the present.",
	},
	{
		name:    "stale-cross-reference",
		pattern: regexp.MustCompile(`(?i)\bthis (bullet|section|document|doc|paragraph|note) (previously|originally|earlier)\b|\b(an?|the) earlier (draft|note|version|section) of this\b|\bthe earlier (draft|note|version)\b|\bpreviously said\b|\bused to say\b`),
		remedy:  "a reader cannot see what the document used to say, so the reference resolves to nothing. State the current decision; git history holds the rest. A supersede marker is fine when it names a file, a section or a symbol the reader can find.",
	},
	{
		name:    "session-artefact",
		pattern: regexp.MustCompile(`(?i)\b(crew investigation|investigation pass|not committed|caught in review|this session|at time of writing|in this session)\b`),
		remedy:  "how the finding was produced is not design. State what is true and the evidence for it.",
	},
}

// codeFence tracks fenced blocks so a rule never fires on quoted sample output
// or a code listing, where a line number or a status string may be the literal
// content being specified.
var codeFence = regexp.MustCompile("^\\s*```")

// exemptHeader matches the two metadata fields that carry a status and a date
// by design (RULES.md gives three statuses). The match is deliberately narrow:
// a Date field holding only a date, and a Status field holding only one of the
// three. A wider exemption let "**Date:** 2026-09-03, Channel resolved
// 2026-09-22" smuggle a dated status into the one line the rules did not read.
var exemptHeader = regexp.MustCompile(`^\*\*Date:\*\*\s*20\d\d-\d\d-\d\d\s*$|^\*\*Status:\*\*\s*(Draft|Approved|Finalised)\s*$`)

// statusField matches the document's own status line, whatever it holds.
var statusField = regexp.MustCompile(`^\*\*Status:\*\*\s*(.*)$`)

// statuses is the closed set from RULES.md. A status outside it is how a
// progress report gets into the one field the rules cannot read: "Implemented
// (wiring landed)" and "Ratified (rev. 2e). Rev. 2e adds..." were both really
// changelogs.
var statuses = map[string]bool{"Draft": true, "Approved": true, "Finalised": true}

// statusRemedy is the status rule's fix. It is not in rules because the check
// is document-level: a missing field has no line to point at.
const statusRemedy = "every PRD carries exactly one of Draft, Approved or Finalised. Anything else is a progress report in the one field the other rules exempt."

// LintDocument reports every violation in a whole document: the status the
// document must declare, then each line.
func LintDocument(name, content string) []Finding {
	return append(lintStatus(name, content), Lint(name, content)...)
}

// Lint reports every line-level rule violation in one document.
func Lint(name, content string) []Finding {
	var findings []Finding
	inFence := false
	for i, line := range strings.Split(content, "\n") {
		if codeFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence || exemptHeader.MatchString(line) {
			continue
		}
		for _, r := range rules {
			if r.pattern.MatchString(line) {
				findings = append(findings, Finding{File: name, Line: i + 1, Rule: r.name, Text: line})
			}
		}
	}
	return findings
}

// lintStatus checks the one status line RULES.md requires, since a missing or
// free-text status is not a line-level violation any pattern above would see.
func lintStatus(name, content string) []Finding {
	for i, line := range strings.Split(content, "\n") {
		m := statusField.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if statuses[strings.TrimSpace(m[1])] {
			return nil
		}
		return []Finding{{File: name, Line: i + 1, Rule: "status", Text: line}}
	}
	return []Finding{{File: name, Line: 1, Rule: "status", Text: "no **Status:** field"}}
}
