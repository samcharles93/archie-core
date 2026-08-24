package workflow

import (
	"errors"
	"fmt"
)

// ReviewLevel represents the severity level of a review finding.
// In house style, "error" blocks PR creation when confirmed; "warn" is advisory.
type ReviewLevel string

const (
	// ReviewLevelError indicates a severe defect that blocks workflow execution when confirmed.
	ReviewLevelError ReviewLevel = "error"
	// ReviewLevelWarn indicates an advisory finding that is logged but never blocks.
	ReviewLevelWarn ReviewLevel = "warn"
)

// ReviewVerdict distinguishes confirmed defects from plausible worries.
// The load-bearing rule is that ONLY a confirmed defect may block.
type ReviewVerdict string

const (
	// ReviewVerdictConfirmed indicates a verified defect backed by a concrete failure scenario.
	ReviewVerdictConfirmed ReviewVerdict = "confirmed"
	// ReviewVerdictPlausible indicates a potential defect or worry that is plausible but unverified.
	ReviewVerdictPlausible ReviewVerdict = "plausible"
)

// ReviewDisposition records what happened once a finding was acted on.
type ReviewDisposition string

const (
	// ReviewDispositionFixed indicates the defect was addressed and resolved in code.
	ReviewDispositionFixed ReviewDisposition = "fixed"
	// ReviewDispositionSkipped indicates the finding was reviewed and intentionally bypassed.
	ReviewDispositionSkipped ReviewDisposition = "skipped"
	// ReviewDispositionNoChangeNeeded indicates the finding was investigated and judged as requiring no change.
	ReviewDispositionNoChangeNeeded ReviewDisposition = "no_change_needed"
)

// ReviewCategory classifies findings against this repo's review checklist.
type ReviewCategory string

const (
	// ReviewCategoryDeadCode identifies unreferenced or dead code paths.
	ReviewCategoryDeadCode ReviewCategory = "dead-code"
	// ReviewCategoryUncheckedError identifies ignored or unchecked error returns.
	ReviewCategoryUncheckedError ReviewCategory = "unchecked-error"
	// ReviewCategoryHardcodedValue identifies hardcoded values that should be parameters or constants.
	ReviewCategoryHardcodedValue ReviewCategory = "hardcoded-value"
	// ReviewCategoryInterfaceSatisfaction identifies interface compliance or satisfaction defects.
	ReviewCategoryInterfaceSatisfaction ReviewCategory = "interface-satisfaction"
	// ReviewCategoryNilRisk identifies nil-pointer dereference risks.
	ReviewCategoryNilRisk ReviewCategory = "nil-risk"
	// ReviewCategoryGoroutineLeak identifies leaking goroutines or unbound concurrency.
	ReviewCategoryGoroutineLeak ReviewCategory = "goroutine-leak"
	// ReviewCategoryRace identifies data races or unsynchronized access to shared mutable state.
	ReviewCategoryRace ReviewCategory = "race"
	// ReviewCategoryOther identifies defects not matching specific checklist items.
	ReviewCategoryOther ReviewCategory = "other"
)

// ReviewStatus represents the execution status of an adversarial review stage.
type ReviewStatus string

const (
	// ReviewStatusNotRun indicates the reviewer agent was not executed.
	ReviewStatusNotRun ReviewStatus = "not_run"
	// ReviewStatusCompleted indicates the reviewer agent ran to completion.
	ReviewStatusCompleted ReviewStatus = "completed"
	// ReviewStatusSkipped indicates the reviewer was intentionally skipped.
	ReviewStatusSkipped ReviewStatus = "skipped"
)

// ReviewFinding represents one defect found during adversarial review.
// It is serialisable across the agent-execution JSON data boundary.
type ReviewFinding struct {
	// File is the repo-relative file path where the defect is located.
	File string `json:"file"`
	// Line is the line number where the defect is located.
	Line int `json:"line"`
	// Defect is a one-sentence statement of the defect.
	Defect string `json:"defect"`
	// FailureScenario describes the concrete inputs or state that produce the wrong output or crash.
	FailureScenario string `json:"failure_scenario"`
	// Verdict distinguishes confirmed defects from plausible hypotheses. Only confirmed findings may block.
	Verdict ReviewVerdict `json:"verdict"`
	// Disposition records the action taken once the finding is addressed.
	Disposition ReviewDisposition `json:"disposition,omitempty"`
	// Level is "error" (blocking when confirmed) or "warn" (advisory).
	Level ReviewLevel `json:"level"`
	// Category classifies the defect according to the review checklist.
	Category ReviewCategory `json:"category"`
}

// ReviewReport represents the aggregate result of an adversarial review.
// It explicitly distinguishes "reviewer looked and found nothing" (Status == ReviewStatusCompleted, Findings empty)
// from "reviewer never ran" (Status == ReviewStatusNotRun or uninitialised).
type ReviewReport struct {
	// Status explicitly records whether the review was run, not run, or skipped.
	Status ReviewStatus `json:"status"`
	// Findings contains all findings produced by the review.
	Findings []ReviewFinding `json:"findings,omitempty"`
	// Summary is a human-readable high-level overview of the review.
	Summary string `json:"summary,omitempty"`
	// SkipReason explains why the review was not run or was skipped.
	SkipReason string `json:"skip_reason,omitempty"`
}

// Blocking reports whether the finding is a blocking defect.
// The load-bearing rule: ONLY a finding with Verdict == ReviewVerdictConfirmed
// AND Level == ReviewLevelError blocks. A plausible finding, however severe
// its level claims to be, never blocks on its own.
func (f ReviewFinding) Blocking() bool {
	return f.Verdict == ReviewVerdictConfirmed && f.Level == ReviewLevelError
}

// ReviewBlocking reports whether any finding in the slice is blocking.
// The load-bearing rule: ONLY a finding with Verdict == ReviewVerdictConfirmed
// and Level == ReviewLevelError blocks. Plausible findings and warn-level findings
// never block.
func ReviewBlocking(findings []ReviewFinding) bool {
	for _, f := range findings {
		if f.Blocking() {
			return true
		}
	}
	return false
}

// Ran reports whether the adversarial reviewer actually executed to completion.
// Reviews that were not run (Status == ReviewStatusNotRun or uninitialised) or skipped
// return false.
func (r ReviewReport) Ran() bool {
	return r.Status == ReviewStatusCompleted
}

// Blocking reports whether any finding within the report is blocking.
func (r ReviewReport) Blocking() bool {
	return ReviewBlocking(r.Findings)
}

// Passed reports whether the review ran to completion and produced no blocking findings.
// A review that did not run (or has zero-value status) returns false because "not run"
// is structurally distinct from "ran and found 0 defects".
func (r ReviewReport) Passed() bool {
	return r.Ran() && !r.Blocking()
}

// Validate checks that the finding satisfies contract invariants.
func (f ReviewFinding) Validate() error {
	if f.File == "" {
		return errors.New("review finding file is required")
	}
	if f.Line < 0 {
		return fmt.Errorf("review finding line cannot be negative: %d", f.Line)
	}
	if f.Defect == "" {
		return errors.New("review finding defect statement is required")
	}
	if f.FailureScenario == "" {
		return errors.New("review finding failure scenario is required")
	}
	switch f.Verdict {
	case ReviewVerdictConfirmed, ReviewVerdictPlausible:
	default:
		return fmt.Errorf("invalid review verdict %q (must be %q or %q)", f.Verdict, ReviewVerdictConfirmed, ReviewVerdictPlausible)
	}
	switch f.Level {
	case ReviewLevelError, ReviewLevelWarn:
	default:
		return fmt.Errorf("invalid review level %q (must be %q or %q)", f.Level, ReviewLevelError, ReviewLevelWarn)
	}
	switch f.Category {
	case ReviewCategoryDeadCode,
		ReviewCategoryUncheckedError,
		ReviewCategoryHardcodedValue,
		ReviewCategoryInterfaceSatisfaction,
		ReviewCategoryNilRisk,
		ReviewCategoryGoroutineLeak,
		ReviewCategoryRace,
		ReviewCategoryOther:
	default:
		return fmt.Errorf("invalid review category %q", f.Category)
	}
	if f.Disposition != "" {
		switch f.Disposition {
		case ReviewDispositionFixed, ReviewDispositionSkipped, ReviewDispositionNoChangeNeeded:
		default:
			return fmt.Errorf("invalid review disposition %q", f.Disposition)
		}
	}
	return nil
}

// Validate checks that the report satisfies contract invariants.
func (r ReviewReport) Validate() error {
	switch r.Status {
	case ReviewStatusNotRun, ReviewStatusCompleted, ReviewStatusSkipped:
	default:
		return fmt.Errorf("invalid review status %q", r.Status)
	}
	for i, f := range r.Findings {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("findings[%d]: %w", i, err)
		}
	}
	return nil
}

// NewCompletedReviewReport constructs a completed review report.
func NewCompletedReviewReport(findings []ReviewFinding, summary string) ReviewReport {
	if findings == nil {
		findings = []ReviewFinding{}
	}
	return ReviewReport{
		Status:   ReviewStatusCompleted,
		Findings: findings,
		Summary:  summary,
	}
}

// NewNotRunReviewReport constructs a report representing an unexecuted review.
func NewNotRunReviewReport(reason string) ReviewReport {
	return ReviewReport{
		Status:     ReviewStatusNotRun,
		SkipReason: reason,
	}
}

// NewSkippedReviewReport constructs a report representing a skipped review.
func NewSkippedReviewReport(reason string) ReviewReport {
	return ReviewReport{
		Status:     ReviewStatusSkipped,
		SkipReason: reason,
	}
}
