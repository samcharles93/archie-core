package workflow

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReviewFindingBlockingMatrix(t *testing.T) {
	tests := []struct {
		name         string
		verdict      ReviewVerdict
		level        ReviewLevel
		wantBlocking bool
	}{
		{
			name:         "confirmed error blocks",
			verdict:      ReviewVerdictConfirmed,
			level:        ReviewLevelError,
			wantBlocking: true,
		},
		{
			name:         "confirmed warn does not block",
			verdict:      ReviewVerdictConfirmed,
			level:        ReviewLevelWarn,
			wantBlocking: false,
		},
		{
			name:         "confirmed empty level does not block",
			verdict:      ReviewVerdictConfirmed,
			level:        "",
			wantBlocking: false,
		},
		{
			name:         "confirmed unknown level does not block",
			verdict:      ReviewVerdictConfirmed,
			level:        "critical",
			wantBlocking: false,
		},
		{
			name:         "plausible error MUST NOT block",
			verdict:      ReviewVerdictPlausible,
			level:        ReviewLevelError,
			wantBlocking: false,
		},
		{
			name:         "plausible warn does not block",
			verdict:      ReviewVerdictPlausible,
			level:        ReviewLevelWarn,
			wantBlocking: false,
		},
		{
			name:         "plausible empty level does not block",
			verdict:      ReviewVerdictPlausible,
			level:        "",
			wantBlocking: false,
		},
		{
			name:         "empty verdict error does not block",
			verdict:      "",
			level:        ReviewLevelError,
			wantBlocking: false,
		},
		{
			name:         "empty verdict warn does not block",
			verdict:      "",
			level:        ReviewLevelWarn,
			wantBlocking: false,
		},
		{
			name:         "empty verdict and level does not block",
			verdict:      "",
			level:        "",
			wantBlocking: false,
		},
		{
			name:         "unknown verdict with error does not block",
			verdict:      "likely",
			level:        ReviewLevelError,
			wantBlocking: false,
		},
		{
			name:         "uppercase confirmed error does not block (case sensitive)",
			verdict:      "CONFIRMED",
			level:        "ERROR",
			wantBlocking: false,
		},
		{
			name:         "title case confirmed error does not block (case sensitive)",
			verdict:      "Confirmed",
			level:        "Error",
			wantBlocking: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := ReviewFinding{
				File:            "internal/domain/workflow/review_findings.go",
				Line:            42,
				Defect:          "sample defect statement",
				FailureScenario: "sample failure scenario",
				Verdict:         tt.verdict,
				Level:           tt.level,
				Category:        ReviewCategoryNilRisk,
			}

			if got := f.Blocking(); got != tt.wantBlocking {
				t.Errorf("ReviewFinding.Blocking() = %v, want %v (verdict=%q, level=%q)",
					got, tt.wantBlocking, tt.verdict, tt.level)
			}
		})
	}
}

func TestBlockingReviewFindingsSlice(t *testing.T) {
	tests := []struct {
		name         string
		findings     []ReviewFinding
		wantBlocking bool
	}{
		{
			name:         "nil slice returns false",
			findings:     nil,
			wantBlocking: false,
		},
		{
			name:         "empty slice returns false",
			findings:     []ReviewFinding{},
			wantBlocking: false,
		},
		{
			name: "single confirmed error returns true",
			findings: []ReviewFinding{
				{
					File:            "main.go",
					Line:            10,
					Defect:          "nil pointer dereference",
					FailureScenario: "running with nil config crashes",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelError,
					Category:        ReviewCategoryNilRisk,
				},
			},
			wantBlocking: true,
		},
		{
			name: "warn-only confirmed findings do not block",
			findings: []ReviewFinding{
				{
					File:            "main.go",
					Line:            10,
					Defect:          "unused helper function",
					FailureScenario: "function is never called",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelWarn,
					Category:        ReviewCategoryDeadCode,
				},
				{
					File:            "util.go",
					Line:            20,
					Defect:          "magic number in loop",
					FailureScenario: "hardcoded 100 instead of constant",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelWarn,
					Category:        ReviewCategoryHardcodedValue,
				},
			},
			wantBlocking: false,
		},
		{
			name: "plausible error findings do not block",
			findings: []ReviewFinding{
				{
					File:            "server.go",
					Line:            50,
					Defect:          "potential race condition",
					FailureScenario: "concurrent writes might race if mutex is missing",
					Verdict:         ReviewVerdictPlausible,
					Level:           ReviewLevelError,
					Category:        ReviewCategoryRace,
				},
			},
			wantBlocking: false,
		},
		{
			name: "mixed findings with one confirmed error returns true",
			findings: []ReviewFinding{
				{
					File:            "doc.go",
					Line:            1,
					Defect:          "typo in comment",
					FailureScenario: "spelling mistake in doc comment",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelWarn,
					Category:        ReviewCategoryOther,
				},
				{
					File:            "handler.go",
					Line:            45,
					Defect:          "unchecked error return from Write",
					FailureScenario: "client disconnect causes unhandled error",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelError,
					Category:        ReviewCategoryUncheckedError,
				},
				{
					File:            "worker.go",
					Line:            100,
					Defect:          "unbounded goroutine launch",
					FailureScenario: "burst of 10k items exhausts memory",
					Verdict:         ReviewVerdictPlausible,
					Level:           ReviewLevelError,
					Category:        ReviewCategoryGoroutineLeak,
				},
			},
			wantBlocking: true,
		},
		{
			name: "confirmed error as first element returns true",
			findings: []ReviewFinding{
				{
					File:            "first.go",
					Line:            1,
					Defect:          "blocking issue",
					FailureScenario: "crashes immediately",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelError,
					Category:        ReviewCategoryNilRisk,
				},
				{
					File:            "second.go",
					Line:            2,
					Defect:          "advisory issue",
					FailureScenario: "minor warning",
					Verdict:         ReviewVerdictConfirmed,
					Level:           ReviewLevelWarn,
					Category:        ReviewCategoryOther,
				},
			},
			wantBlocking: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BlockingReviewFindings(tt.findings); got != tt.wantBlocking {
				t.Errorf("BlockingReviewFindings() = %v, want %v", got, tt.wantBlocking)
			}
			if got := ReviewBlocking(tt.findings); got != tt.wantBlocking {
				t.Errorf("ReviewBlocking() = %v, want %v", got, tt.wantBlocking)
			}
		})
	}

	t.Run("large slice detects confirmed error buried among warns", func(t *testing.T) {
		findings := make([]ReviewFinding, 1000)
		for i := range findings {
			findings[i] = ReviewFinding{
				File:            "file.go",
				Line:            i,
				Defect:          "warn finding",
				FailureScenario: "advisory scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelWarn,
				Category:        ReviewCategoryOther,
			}
		}
		findings[750] = ReviewFinding{
			File:            "file.go",
			Line:            750,
			Defect:          "confirmed error",
			FailureScenario: "fatal crash on nil context",
			Verdict:         ReviewVerdictConfirmed,
			Level:           ReviewLevelError,
			Category:        ReviewCategoryNilRisk,
		}

		if !BlockingReviewFindings(findings) {
			t.Error("BlockingReviewFindings() = false, want true for confirmed error at index 750")
		}
	})
}

func TestReviewReportStatusDistinction(t *testing.T) {
	tests := []struct {
		name         string
		report       ReviewReport
		wantRan      bool
		wantBlocking bool
		wantPassed   bool
	}{
		{
			name: "ran and found zero findings passes",
			report: ReviewReport{
				Status:   ReviewStatusCompleted,
				Findings: []ReviewFinding{},
				Summary:  "adversarial review completed with 0 findings",
			},
			wantRan:      true,
			wantBlocking: false,
			wantPassed:   true,
		},
		{
			name: "ran and found nil findings slice passes",
			report: ReviewReport{
				Status:   ReviewStatusCompleted,
				Findings: nil,
				Summary:  "adversarial review completed cleanly",
			},
			wantRan:      true,
			wantBlocking: false,
			wantPassed:   true,
		},
		{
			name: "ran and found only warn findings passes",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{
						File:            "a.go",
						Line:            12,
						Defect:          "advisory note",
						FailureScenario: "minor style difference",
						Verdict:         ReviewVerdictConfirmed,
						Level:           ReviewLevelWarn,
						Category:        ReviewCategoryOther,
					},
				},
				Summary: "advisories found",
			},
			wantRan:      true,
			wantBlocking: false,
			wantPassed:   true,
		},
		{
			name: "ran and found plausible error findings passes",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{
						File:            "b.go",
						Line:            34,
						Defect:          "hypothetical deadlock",
						FailureScenario: "if channel buffers fill up",
						Verdict:         ReviewVerdictPlausible,
						Level:           ReviewLevelError,
						Category:        ReviewCategoryGoroutineLeak,
					},
				},
				Summary: "plausible error found",
			},
			wantRan:      true,
			wantBlocking: false,
			wantPassed:   true,
		},
		{
			name: "ran and found confirmed error blocks and does not pass",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{
						File:            "c.go",
						Line:            56,
						Defect:          "unhandled error in critical section",
						FailureScenario: "database error causes silent data corruption",
						Verdict:         ReviewVerdictConfirmed,
						Level:           ReviewLevelError,
						Category:        ReviewCategoryUncheckedError,
					},
				},
				Summary: "confirmed blocking error",
			},
			wantRan:      true,
			wantBlocking: true,
			wantPassed:   false,
		},
		{
			name: "did not run MUST NOT be considered passed even with empty findings",
			report: ReviewReport{
				Status:     ReviewStatusNotRun,
				Findings:   []ReviewFinding{},
				SkipReason: "stage not reached due to prior failure",
			},
			wantRan:      false,
			wantBlocking: false,
			wantPassed:   false,
		},
		{
			name: "did not run with nil findings does not pass",
			report: ReviewReport{
				Status:     ReviewStatusNotRun,
				Findings:   nil,
				SkipReason: "pipeline stopped early",
			},
			wantRan:      false,
			wantBlocking: false,
			wantPassed:   false,
		},
		{
			name: "skipped review does not count as ran and does not pass",
			report: ReviewReport{
				Status:     ReviewStatusSkipped,
				Findings:   []ReviewFinding{},
				SkipReason: "documentation-only diff skipped by policy",
			},
			wantRan:      false,
			wantBlocking: false,
			wantPassed:   false,
		},
		{
			name:         "zero-value report does not count as ran and does not pass",
			report:       ReviewReport{},
			wantRan:      false,
			wantBlocking: false,
			wantPassed:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.report.Ran(); got != tt.wantRan {
				t.Errorf("ReviewReport.Ran() = %v, want %v", got, tt.wantRan)
			}
			if got := tt.report.Blocking(); got != tt.wantBlocking {
				t.Errorf("ReviewReport.Blocking() = %v, want %v", got, tt.wantBlocking)
			}
			if got := tt.report.Passed(); got != tt.wantPassed {
				t.Errorf("ReviewReport.Passed() = %v, want %v", got, tt.wantPassed)
			}
		})
	}
}

func TestReviewReportConstructors(t *testing.T) {
	t.Run("NewCompletedReviewReport sets completed status", func(t *testing.T) {
		findings := []ReviewFinding{
			{
				File:            "pkg/foo.go",
				Line:            15,
				Defect:          "hardcoded port",
				FailureScenario: "port 8080 conflicts in container",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelWarn,
				Category:        ReviewCategoryHardcodedValue,
			},
		}
		report := NewCompletedReviewReport(findings, "completed successfully")
		if report.Status != ReviewStatusCompleted {
			t.Errorf("Status = %q, want %q", report.Status, ReviewStatusCompleted)
		}
		if !report.Ran() {
			t.Error("Ran() = false, want true")
		}
		if len(report.Findings) != 1 {
			t.Errorf("len(Findings) = %d, want 1", len(report.Findings))
		}
		if report.Summary != "completed successfully" {
			t.Errorf("Summary = %q, want %q", report.Summary, "completed successfully")
		}
	})

	t.Run("NewCompletedReviewReport with nil findings initializes empty slice", func(t *testing.T) {
		report := NewCompletedReviewReport(nil, "clean run")
		if report.Findings == nil {
			t.Error("Findings should be non-nil empty slice")
		}
		if len(report.Findings) != 0 {
			t.Errorf("len(Findings) = %d, want 0", len(report.Findings))
		}
		if !report.Passed() {
			t.Error("Passed() = false, want true for clean completed review")
		}
	})

	t.Run("NewNotRunReviewReport sets not_run status", func(t *testing.T) {
		report := NewNotRunReviewReport("prerequisite stage failed")
		if report.Status != ReviewStatusNotRun {
			t.Errorf("Status = %q, want %q", report.Status, ReviewStatusNotRun)
		}
		if report.Ran() {
			t.Error("Ran() = true, want false")
		}
		if report.Passed() {
			t.Error("Passed() = true, want false")
		}
		if report.SkipReason != "prerequisite stage failed" {
			t.Errorf("SkipReason = %q, want %q", report.SkipReason, "prerequisite stage failed")
		}
	})

	t.Run("NewSkippedReviewReport sets skipped status", func(t *testing.T) {
		report := NewSkippedReviewReport("markdown change only")
		if report.Status != ReviewStatusSkipped {
			t.Errorf("Status = %q, want %q", report.Status, ReviewStatusSkipped)
		}
		if report.Ran() {
			t.Error("Ran() = true, want false")
		}
		if report.Passed() {
			t.Error("Passed() = true, want false")
		}
		if report.SkipReason != "markdown change only" {
			t.Errorf("SkipReason = %q, want %q", report.SkipReason, "markdown change only")
		}
	})
}

func TestReviewFindingJSONRoundTrip(t *testing.T) {
	original := ReviewFinding{
		File:            "internal/domain/workflow/steps.go",
		Line:            142,
		Defect:          "Goroutine spawned without error channel drain causes leak",
		FailureScenario: "When context is cancelled before worker exits, the goroutine remains blocked on ch <- result forever",
		Verdict:         ReviewVerdictConfirmed,
		Disposition:     ReviewDispositionFixed,
		Level:           ReviewLevelError,
		Category:        ReviewCategoryGoroutineLeak,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(ReviewFinding) failed: %v", err)
	}

	var decoded ReviewFinding
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ReviewFinding) failed: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Errorf("JSON round-trip mismatch:\n got:  %#v\n want: %#v", decoded, original)
	}

	// Explicitly assert that verdict and disposition survived the round trip
	if decoded.Verdict != ReviewVerdictConfirmed {
		t.Errorf("Verdict = %q, want %q", decoded.Verdict, ReviewVerdictConfirmed)
	}
	if decoded.Disposition != ReviewDispositionFixed {
		t.Errorf("Disposition = %q, want %q", decoded.Disposition, ReviewDispositionFixed)
	}
	if decoded.Level != ReviewLevelError {
		t.Errorf("Level = %q, want %q", decoded.Level, ReviewLevelError)
	}
	if decoded.Category != ReviewCategoryGoroutineLeak {
		t.Errorf("Category = %q, want %q", decoded.Category, ReviewCategoryGoroutineLeak)
	}
	if !decoded.Blocking() {
		t.Error("Decoded finding should remain blocking after round-trip")
	}
}

func TestReviewReportJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		report ReviewReport
	}{
		{
			name: "completed with multiple findings",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{
						File:            "pkg/auth/token.go",
						Line:            88,
						Defect:          "Missing signature verification before claims decoding",
						FailureScenario: "Forged JWT with 'none' alg is accepted as valid",
						Verdict:         ReviewVerdictConfirmed,
						Disposition:     ReviewDispositionNoChangeNeeded,
						Level:           ReviewLevelError,
						Category:        ReviewCategoryInterfaceSatisfaction,
					},
					{
						File:            "pkg/auth/token.go",
						Line:            120,
						Defect:          "Debug log prints raw token",
						FailureScenario: "Verbose logging exposes bearer token in daemon logs",
						Verdict:         ReviewVerdictPlausible,
						Disposition:     ReviewDispositionSkipped,
						Level:           ReviewLevelWarn,
						Category:        ReviewCategoryOther,
					},
				},
				Summary: "Found 1 confirmed defect and 1 advisory",
			},
		},
		{
			name: "completed with zero findings",
			report: ReviewReport{
				Status:   ReviewStatusCompleted,
				Findings: []ReviewFinding{},
				Summary:  "Clean review pass",
			},
		},
		{
			name: "not run report",
			report: ReviewReport{
				Status:     ReviewStatusNotRun,
				SkipReason: "gate failed prior to review",
			},
		},
		{
			name: "skipped report",
			report: ReviewReport{
				Status:     ReviewStatusSkipped,
				SkipReason: "doc changes only",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.report)
			if err != nil {
				t.Fatalf("json.Marshal(ReviewReport) failed: %v", err)
			}

			var decoded ReviewReport
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("json.Unmarshal(ReviewReport) failed: %v", err)
			}

			// For empty slice, JSON unmarshals as nil if omitted or [] if present.
			// Compare semantic state.
			if decoded.Status != tt.report.Status {
				t.Errorf("Status = %q, want %q", decoded.Status, tt.report.Status)
			}
			if decoded.Summary != tt.report.Summary {
				t.Errorf("Summary = %q, want %q", decoded.Summary, tt.report.Summary)
			}
			if decoded.SkipReason != tt.report.SkipReason {
				t.Errorf("SkipReason = %q, want %q", decoded.SkipReason, tt.report.SkipReason)
			}
			if decoded.Ran() != tt.report.Ran() {
				t.Errorf("Ran() = %v, want %v", decoded.Ran(), tt.report.Ran())
			}
			if decoded.Blocking() != tt.report.Blocking() {
				t.Errorf("Blocking() = %v, want %v", decoded.Blocking(), tt.report.Blocking())
			}
			if decoded.Passed() != tt.report.Passed() {
				t.Errorf("Passed() = %v, want %v", decoded.Passed(), tt.report.Passed())
			}
			if len(decoded.Findings) != len(tt.report.Findings) {
				t.Fatalf("len(Findings) = %d, want %d", len(decoded.Findings), len(tt.report.Findings))
			}
			for i := range decoded.Findings {
				if !reflect.DeepEqual(decoded.Findings[i], tt.report.Findings[i]) {
					t.Errorf("Findings[%d] mismatch:\n got:  %#v\n want: %#v",
						i, decoded.Findings[i], tt.report.Findings[i])
				}
			}
		})
	}
}

func TestReviewFindingValidation(t *testing.T) {
	tests := []struct {
		name      string
		finding   ReviewFinding
		wantError bool
	}{
		{
			name: "valid full finding with confirmed verdict and error level",
			finding: ReviewFinding{
				File:            "internal/domain/workflow/steps.go",
				Line:            42,
				Defect:          "Nil check missing before dereferencing context",
				FailureScenario: "Calling step with nil context causes panic",
				Verdict:         ReviewVerdictConfirmed,
				Disposition:     ReviewDispositionFixed,
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: false,
		},
		{
			name: "valid finding without disposition (new unacted finding)",
			finding: ReviewFinding{
				File:            "internal/domain/workflow/steps.go",
				Line:            10,
				Defect:          "Unchecked error return",
				FailureScenario: "Write failure ignored silently",
				Verdict:         ReviewVerdictPlausible,
				Level:           ReviewLevelWarn,
				Category:        ReviewCategoryUncheckedError,
			},
			wantError: false,
		},
		{
			name: "valid finding with line 0",
			finding: ReviewFinding{
				File:            "README.md",
				Line:            0,
				Defect:          "Missing architecture section",
				FailureScenario: "New developers cannot find component map",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelWarn,
				Category:        ReviewCategoryOther,
			},
			wantError: false,
		},
		{
			name: "all valid checklist categories accepted",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            1,
				Defect:          "Valid defect",
				FailureScenario: "Valid scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelError,
				Category:        ReviewCategoryDeadCode,
			},
			wantError: false,
		},
		{
			name: "invalid empty file",
			finding: ReviewFinding{
				File:            "",
				Line:            10,
				Defect:          "Defect without file",
				FailureScenario: "Scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
		{
			name: "invalid negative line",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            -5,
				Defect:          "Negative line finding",
				FailureScenario: "Scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
		{
			name: "invalid empty defect",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            10,
				Defect:          "",
				FailureScenario: "Scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
		{
			name: "invalid empty failure scenario",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            10,
				Defect:          "Defect without scenario",
				FailureScenario: "",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
		{
			name: "invalid verdict",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            10,
				Defect:          "Defect with bogus verdict",
				FailureScenario: "Scenario",
				Verdict:         "maybe",
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
		{
			name: "invalid level",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            10,
				Defect:          "Defect with bogus level",
				FailureScenario: "Scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           "critical",
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
		{
			name: "invalid category",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            10,
				Defect:          "Defect with bogus category",
				FailureScenario: "Scenario",
				Verdict:         ReviewVerdictConfirmed,
				Level:           ReviewLevelError,
				Category:        "unknown-category",
			},
			wantError: true,
		},
		{
			name: "invalid disposition",
			finding: ReviewFinding{
				File:            "file.go",
				Line:            10,
				Defect:          "Defect with bogus disposition",
				FailureScenario: "Scenario",
				Verdict:         ReviewVerdictConfirmed,
				Disposition:     "ignored",
				Level:           ReviewLevelError,
				Category:        ReviewCategoryNilRisk,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.finding.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("ReviewFinding.Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestReviewReportValidation(t *testing.T) {
	tests := []struct {
		name      string
		report    ReviewReport
		wantError bool
	}{
		{
			name: "valid completed report with valid findings",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{
						File:            "file.go",
						Line:            10,
						Defect:          "Valid defect",
						FailureScenario: "Valid scenario",
						Verdict:         ReviewVerdictConfirmed,
						Level:           ReviewLevelError,
						Category:        ReviewCategoryNilRisk,
					},
				},
				Summary: "valid summary",
			},
			wantError: false,
		},
		{
			name: "valid not_run report",
			report: ReviewReport{
				Status:     ReviewStatusNotRun,
				SkipReason: "gate failed",
			},
			wantError: false,
		},
		{
			name: "valid skipped report",
			report: ReviewReport{
				Status:     ReviewStatusSkipped,
				SkipReason: "policy skip",
			},
			wantError: false,
		},
		{
			name: "invalid report status",
			report: ReviewReport{
				Status: "in_progress",
			},
			wantError: true,
		},
		{
			name: "invalid finding inside findings slice",
			report: ReviewReport{
				Status: ReviewStatusCompleted,
				Findings: []ReviewFinding{
					{
						File:            "",
						Line:            10,
						Defect:          "Missing file",
						FailureScenario: "Scenario",
						Verdict:         ReviewVerdictConfirmed,
						Level:           ReviewLevelError,
						Category:        ReviewCategoryNilRisk,
					},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.report.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("ReviewReport.Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}
