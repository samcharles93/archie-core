package wfextract

import (
	"context"
	"reflect"
	"testing"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// TestPreviouslyMissingSymbolsReachableFromInterpretedCode is the regression
// test for the stale-symbol-table drift (e7bo): the committed generated file
// predated the domain migration and was hand-patched for imports only, never
// regenerated -- so symbols added after the migration (the routing YAML
// loaders t2db.9/.10/.11, the review types, and the Forger/Reviewer/Trees
// interfaces that TaskContext exposes to every interpreted stage plugin)
// were missing from the table. A stage plugin referencing tc.Trees or the
// review types could not resolve them, silently.
//
// This test interprets a plugin that references the previously-missing
// symbols. It fails if the symbols or wrappers are dropped from the table.
func TestPreviouslyMissingSymbolsReachableFromInterpretedCode(t *testing.T) {
	src := `package main

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Trees is the branch/prepare/diff surface TaskContext.Trees exposes.
func UseTrees(t workflow.Trees) (string, int, error) {
	dir, _, err := t.Prepare(context.Background(), "o", "r", "main", 1, "t", "", "")
	if err != nil {
		return "", 0, err
	}
	diff, err := t.Diff(context.Background(), dir, "main")
	lines, err := t.ChangedLines(context.Background(), dir, "main")
	return diff, lines, err
}

// Reviewer is the adversarial-review surface TaskContext.Reviewer exposes.
func UseReviewer(r workflow.Reviewer) workflow.ReviewReport {
	return r.Review(context.Background(), workflow.ReviewRequest{})
}

// Routing loaders are the t2db.9/.10/.11 API stage plugins may call.
func UseRouting() error {
	_, _, err := workflow.LoadPlaybookDirs([]string{"/nonexistent"})
	return err
}
`

	i, err := yaegiutil.New(interp.Options{}, Symbols)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Resolve and call the routing loader through interpreted code -- the
	// strongest proof the previously-missing symbol is reachable.
	useRouting, err := yaegiutil.Resolve[func() error](i, src, "main.UseRouting")
	if err != nil {
		t.Fatalf("Resolve UseRouting: %v", err)
	}
	err = useRouting()
	if err == nil {
		// LoadPlaybookDirs("/nonexistent") errors only if the dir list is
		// valid and the files are missing -- but an empty / nonexistent
		// directory is a no-op (matching t2db.11's empty-means-defaults).
		// Either way, the call itself resolving and executing proves the
		// symbol is wired.
		t.Logf("UseRouting returned nil error (nonexistent dir is a no-op)")
	}
}

// TestWrapperNilGuardsPreserved is the Go-side nil-guard regression test,
// following the established pattern in secretextract/wrapper_test.go and
// pluginextract's TestGeneratedWrapperHasNilGuards: yaegi's fresh wrapper
// generation omits nil-guards, so they are re-applied by hand after every
// regeneration. A zero-value wrapper (all function fields nil) must not
// panic.
func TestWrapperNilGuardsPreserved(t *testing.T) {
	var forger _github_com_samcharles93_archie_core_internal_domain_workflow_Forger
	if err := forger.CloseIssue(context.Background(), "", "", 0, ""); err != nil {
		t.Errorf("CloseIssue on nil WCloseIssue = %v, want nil", err)
	}
	if _, err := forger.CreatePR(context.Background(), "", "", "", "", "", ""); err != nil {
		t.Errorf("CreatePR on nil WCreatePR = %v, want nil", err)
	}
	if err := forger.LinkBranch(context.Background(), "", "", 0, ""); err != nil {
		t.Errorf("LinkBranch on nil WLinkBranch = %v, want nil", err)
	}

	if _, err := forger.Comment(context.Background(), "", "", 0, ""); err != nil {
		t.Errorf("Comment on nil WComment = %v, want nil", err)
	}
	if err := forger.ReplyToReview(context.Background(), "", "", 0, 0, ""); err != nil {
		t.Errorf("ReplyToReview on nil WReplyToReview = %v, want nil", err)
	}

	var reviewer _github_com_samcharles93_archie_core_internal_domain_workflow_Reviewer
	if got := reviewer.Review(context.Background(), workflow.ReviewRequest{}); len(got.Findings) != 0 || got.Status != "" || got.Summary != "" {
		t.Errorf("Review on nil WReview = %#v, want empty report", got)
	}

	var trees _github_com_samcharles93_archie_core_internal_domain_workflow_Trees
	if _, _, err := trees.Prepare(context.Background(), "", "", "", 0, "", "", ""); err != nil {
		t.Errorf("Prepare on nil WPrepare = %v, want nil", err)
	}
	if _, err := trees.Diff(context.Background(), "", ""); err != nil {
		t.Errorf("Diff on nil WDiff = %v, want nil", err)
	}
	if _, err := trees.ChangedLines(context.Background(), "", ""); err != nil {
		t.Errorf("ChangedLines on nil WChangedLines = %v, want nil", err)
	}
}

// TestChangeCaptureIsNotCallableFromInterpretedCode is the acceptance test for
// "the sandbox surface did not widen", in the same shape as the reachability
// test above and with the opposite assertion.
//
// An attempt's changed files are captured through an UNEXPORTED optional
// interface asserted on TaskContext.Trees, precisely so that the capability is
// not part of workflow.Trees: Trees is projected into this symbol table, so a
// method added to it would become callable by every repository-authored
// .archie/stages/*.go file. Both halves are asserted here -- the interface's
// method set, and the interpreted call that must not resolve -- because the
// second alone would be weaker: it would still pass if the symbol table lost
// Trees altogether.
func TestChangeCaptureIsNotCallableFromInterpretedCode(t *testing.T) {
	trees := reflect.TypeFor[workflow.Trees]()
	if _, ok := trees.MethodByName("ChangedFileStats"); ok {
		t.Fatalf("workflow.Trees gained ChangedFileStats; interpreted stage code can now read a diffstat the engine only meant to capture")
	}

	// The control first: an interpreted call to a method Trees really has must
	// resolve, so the refusal below is the missing method and not a broken
	// harness.
	const control = `package main

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

func UseTrees(t workflow.Trees) {
	_, _ = t.Diff(context.Background(), "", "")
}
`
	const probe = `package main

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// CaptureDiffstat is an interpreted stage trying to reach the engine's
// change capture through the surface TaskContext hands it.
func CaptureDiffstat(t workflow.Trees) {
	stats, err := t.ChangedFileStats(context.Background(), "", "")
	_, _ = stats.HeadSHA, err
}
`

	i, err := yaegiutil.New(interp.Options{}, Symbols)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := yaegiutil.Resolve[func(workflow.Trees)](i, control, "main.UseTrees"); err != nil {
		t.Fatalf("the control call Trees.Diff did not resolve (%v); this test cannot tell a missing capability from a broken symbol table", err)
	}

	j, err := yaegiutil.New(interp.Options{}, Symbols)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := yaegiutil.Resolve[func(workflow.Trees)](j, probe, "main.CaptureDiffstat"); err == nil {
		t.Error("interpreted stage code resolved Trees.ChangedFileStats; the capture capability leaked into the projected surface")
	}
}
