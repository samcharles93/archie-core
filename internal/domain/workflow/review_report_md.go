package workflow

import (
	"fmt"
	"strings"
)

// RenderMarkdown renders an adversarial review report as the artifact source
// the collaborative editor publishes. It is a pure function over the report:
// no I/O, no infrastructure, so the rendered form is testable in isolation.
//
// A completed review renders its summary, every finding as its own subsection
// (file, line, verdict, level, failure scenario, and the mechanical suggestion
// and disposition when present) and the verified properties as a "checked
// clean" list. A review that did not run renders only its reason, because a
// report that never executed has no findings or checks to stand behind.
func (r ReviewReport) RenderMarkdown(title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)

	if r.Status == ReviewStatusNotRun || (r.SkipReason != "" && len(r.Findings) == 0 && len(r.Checked) == 0) {
		fmt.Fprintf(&b, "> This review did not run: %s\n", r.SkipReason)
		return strings.TrimRight(b.String(), "\n") + "\n"
	}

	if r.Summary != "" {
		b.WriteString(r.Summary)
		b.WriteString("\n\n")
	}

	if len(r.Findings) > 0 {
		b.WriteString("## Findings\n\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "### %s:%d — %s (%s)\n\n", f.File, f.Line, f.Level, f.Verdict)
			fmt.Fprintf(&b, "**Defect:** %s\n\n", f.Defect)
			if f.FailureScenario != "" {
				fmt.Fprintf(&b, "**Failure scenario:** %s\n\n", f.FailureScenario)
			}
			if f.Suggestion != "" {
				fmt.Fprintf(&b, "Suggested fix: %s\n\n", f.Suggestion)
			}
			if f.Disposition != "" {
				fmt.Fprintf(&b, "Disposition: %s\n\n", f.Disposition)
			}
		}
	}

	if len(r.Checked) > 0 {
		b.WriteString("## Checked clean\n\n")
		for _, c := range r.Checked {
			fmt.Fprintf(&b, "- %s — %s\n", c.Property, c.Evidence)
		}
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}
