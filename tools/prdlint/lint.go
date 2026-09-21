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
		pattern: regexp.MustCompile(`(?i)\b(status|resolved|settled|decided|agreed|approved|shipped|landed|complete|completed|done|wired|verified|corrected|confirmed)\b[^.\n]{0,30}\b20\d\d-\d\d-\d\d\b`),
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
		name:    "session-artefact",
		pattern: regexp.MustCompile(`(?i)\b(crew investigation|investigation pass|not committed|caught in review|this session|at time of writing|in this session)\b`),
		remedy:  "how the finding was produced is not design. State what is true and the evidence for it.",
	},
}

// codeFence tracks fenced blocks so a rule never fires on quoted sample output
// or a code listing, where a line number or a status string may be the literal
// content being specified.
var codeFence = regexp.MustCompile("^\\s*```")

// headerField matches the leading metadata block's own fields. A PRD records
// its own status and date there by design (RULES.md gives three statuses), so
// those lines are the one place a date and a status belong.
var headerField = regexp.MustCompile(`^\*\*(Status|Date|Parent|Blocked on|Supersedes|Superseded by):\*\*`)

// Lint reports every rule violation in one document.
func Lint(name, content string) []Finding {
	var findings []Finding
	inFence := false
	for i, line := range strings.Split(content, "\n") {
		if codeFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence || headerField.MatchString(line) {
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
