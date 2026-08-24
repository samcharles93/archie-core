package worktree

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// slogCaptureHandler records all log records for asserting log output.
type slogCaptureHandler struct {
	records []slog.Record
}

func (h *slogCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *slogCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *slogCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *slogCaptureHandler) WithGroup(string) slog.Handler      { return h }

func setSlogCapture(t *testing.T) *slogCaptureHandler {
	t.Helper()
	prev := slog.Default()
	h := &slogCaptureHandler{}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// TestPushSucceedsWhenPostPublicationMetadataFails covers BUG 1 (archie-core-1ysw.6).
// A post-publication upstream metadata failure (e.g. read-only config or corrupted config)
// must NOT cause Push to report an error when the remote ref was successfully published.
// The failure must be logged as a warning instead of failing the task.
func TestPushSucceedsWhenPostPublicationMetadataFails(t *testing.T) {
	tests := []struct {
		name       string
		issue      int
		title      string
		labels     string
		file       string
		content    string
		commitMsg  string
		configPerm os.FileMode
		useLogger  bool
	}{
		{
			name:       "read-only config causes warning with default logger but push succeeds",
			issue:      101,
			title:      "feat: push robustness",
			labels:     "feature",
			file:       "feature.txt",
			content:    "data\n",
			commitMsg:  "feat: push robustness",
			configPerm: 0o400,
			useLogger:  false,
		},
		{
			name:       "read-only config causes warning with manager logger but push succeeds",
			issue:      102,
			title:      "feat: custom logger push",
			labels:     "feature",
			file:       "custom.txt",
			content:    "custom data\n",
			commitMsg:  "feat: custom logger push",
			configPerm: 0o400,
			useLogger:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := setSlogCapture(t)
			ctx := context.Background()
			host := newLocalRemote(t, "acme", "push-test")
			m := newManager(t, host)
			if tc.useLogger {
				m.Log = slog.New(logs)
			}

			dir, branch, err := m.Prepare(ctx, "acme", "push-test", testBase, tc.issue, tc.title, "", tc.labels)
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}

			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.content), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			committed, err := m.CommitAll(ctx, dir, tc.commitMsg)
			if err != nil {
				t.Fatalf("CommitAll() error = %v", err)
			}
			if !committed {
				t.Fatal("CommitAll() = false, want true")
			}

			// Make .git/config read-only so SetConfig will fail when recording upstream.
			cfgPath := filepath.Join(dir, ".git", "config")
			if err := os.Chmod(cfgPath, tc.configPerm); err != nil {
				t.Fatalf("Chmod() error = %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(cfgPath, 0o644) })

			// Push should succeed despite the local metadata failure.
			err = m.Push(ctx, dir, branch)
			if err != nil {
				t.Fatalf("Push() error = %v, want nil (post-publication metadata failure should not fail push)", err)
			}

			// Remote repository MUST have the branch published.
			bare := filepath.Join(host, "acme", "push-test.git")
			remote, err := git.PlainOpen(bare)
			if err != nil {
				t.Fatalf("open bare remote: %v", err)
			}
			if _, err := remote.Reference(plumbing.NewBranchReferenceName(branch), true); err != nil {
				t.Fatalf("branch %q not published on remote after Push: %v", branch, err)
			}

			// A warning log must have been recorded for the upstream metadata failure.
			var warningFound bool
			for _, r := range logs.records {
				if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "upstream") {
					warningFound = true
					break
				}
			}
			if !warningFound {
				t.Errorf("expected warning log about upstream metadata failure, got records: %v", logs.records)
			}
		})
	}
}

// TestRecordUpstreamErrorPaths explicitly tests the error paths of recordUpstream.
func TestRecordUpstreamErrorPaths(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string)
		wantSubstr string
	}{
		{
			name: "write repo config fails when config is read only",
			setup: func(t *testing.T, dir string) {
				cfgPath := filepath.Join(dir, ".git", "config")
				if err := os.Chmod(cfgPath, 0o400); err != nil {
					t.Fatalf("Chmod config read-only error = %v", err)
				}
				t.Cleanup(func() { _ = os.Chmod(cfgPath, 0o644) })
			},
			wantSubstr: "write repo config",
		},
		{
			name: "read repo config fails when config is corrupted",
			setup: func(t *testing.T, dir string) {
				cfgPath := filepath.Join(dir, ".git", "config")
				if err := os.WriteFile(cfgPath, []byte("[unclosed section\nkey ="), 0o644); err != nil {
					t.Fatalf("corrupt config error = %v", err)
				}
			},
			wantSubstr: "read repo config",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			host := newLocalRemote(t, "acme", "record-upstream")
			m := newManager(t, host)

			dir, branch, err := m.Prepare(ctx, "acme", "record-upstream", testBase, 105, "feat: record upstream", "", "feature")
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}

			r, err := git.PlainOpen(dir)
			if err != nil {
				t.Fatalf("PlainOpen() error = %v", err)
			}

			tc.setup(t, dir)

			ref := plumbing.NewBranchReferenceName(branch)
			err = m.recordUpstream(r, branch, ref)
			if err == nil {
				t.Fatalf("recordUpstream() error = nil, want error containing %q", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("recordUpstream() error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestCommitAllHonoursCancellation covers BUG 2 (archie-core-1ysw.8).
// A cancelled context must abort CommitAll before mutating index or HEAD.
func TestCommitAllHonoursCancellation(t *testing.T) {
	tests := []struct {
		name      string
		cancelPre bool
		timeout   bool
		wantError error
	}{
		{
			name:      "pre-cancelled context returns context.Canceled without mutating index or HEAD",
			cancelPre: true,
			wantError: context.Canceled,
		},
		{
			name:      "expired deadline returns context.DeadlineExceeded without mutating index or HEAD",
			timeout:   true,
			wantError: context.DeadlineExceeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			host := newLocalRemote(t, "acme", "cancel-test")
			m := newManager(t, host)

			dir, _, err := m.Prepare(ctx, "acme", "cancel-test", testBase, 201, "feat: cancel test", "", "feature")
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}

			// Capture HEAD commit before attempting any commit.
			r, err := git.PlainOpen(dir)
			if err != nil {
				t.Fatalf("PlainOpen() error = %v", err)
			}
			headBefore, err := r.Head()
			if err != nil {
				t.Fatalf("Head() before error = %v", err)
			}

			// Write a new file to the worktree.
			newFile := filepath.Join(dir, "work.txt")
			if err := os.WriteFile(newFile, []byte("uncommitted work\n"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			var testCtx context.Context
			var cancel context.CancelFunc
			if tc.cancelPre {
				testCtx, cancel = context.WithCancel(context.Background())
				cancel() // Pre-cancel
			} else if tc.timeout {
				var timeoutCancel context.CancelFunc
				testCtx, timeoutCancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				cancel = timeoutCancel
			}
			if cancel != nil {
				defer cancel()
			}

			committed, commitErr := m.CommitAll(testCtx, dir, "feat: should be aborted")
			if commitErr == nil {
				t.Fatalf("CommitAll() error = nil, want %v", tc.wantError)
			}
			if !errors.Is(commitErr, tc.wantError) {
				t.Fatalf("CommitAll() error = %v, want errors.Is(..., %v)", commitErr, tc.wantError)
			}
			if committed {
				t.Errorf("CommitAll() returned committed=true on cancelled context, want false")
			}

			// Verify HEAD commit has NOT moved.
			headAfter, err := r.Head()
			if err != nil {
				t.Fatalf("Head() after error = %v", err)
			}
			if headBefore.Hash() != headAfter.Hash() {
				t.Errorf("HEAD mutated: before=%s, after=%s", headBefore.Hash(), headAfter.Hash())
			}

			// Verify index has NOT staged the new file (worktree status should show it untracked).
			wt, err := r.Worktree()
			if err != nil {
				t.Fatalf("Worktree() error = %v", err)
			}
			status, err := wt.Status()
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			fileStatus := status.File("work.txt")
			if fileStatus.Staging != git.Untracked {
				t.Errorf("work.txt Staging status = %v, want Untracked (no index mutation)", fileStatus.Staging)
			}
		})
	}
}

// TestBranchNamingPrecedence covers BUG 3 (archie-core-1ysw.9).
// Tests title-vs-label precedence, dead parameter removal, no-op removal,
// and 60-char slug boundary agreement.
func TestBranchNamingPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		issue  int
		title  string
		labels string
		want   string
	}{
		{
			name:   "unlabeled issue with conventional prefix in title derives prefix from title",
			issue:  12,
			title:  "fix: crash on empty payload",
			labels: "",
			want:   "fix/12-crash-on-empty-payload",
		},
		{
			name:   "unlabeled issue with scoped conventional prefix in title",
			issue:  13,
			title:  "chore(deps): update go to 1.26",
			labels: "",
			want:   "chore/13-update-go-to-126",
		},
		{
			name:   "title conventional prefix takes precedence when title and labels disagree",
			issue:  14,
			title:  "fix: wrong calculation",
			labels: "feature,enhancement",
			want:   "fix/14-wrong-calculation",
		},
		{
			name:   "title feat takes precedence when label says bug",
			issue:  15,
			title:  "feat: new export option",
			labels: "bug",
			want:   "feat/15-new-export-option",
		},
		{
			name:   "unlabeled issue without conventional prefix falls back to default feat",
			issue:  16,
			title:  "plain title without prefix",
			labels: "",
			want:   "feat/16-plain-title-without-prefix",
		},
		{
			name:   "title without prefix falls back to label",
			issue:  17,
			title:  "unexpected crash on startup",
			labels: "bug",
			want:   "fix/17-unexpected-crash-on-startup",
		},
		{
			name:   "title without prefix with docs label",
			issue:  18,
			title:  "update installation instructions",
			labels: "docs",
			want:   "docs/18-update-installation-instructions",
		},
		{
			name:   "title without prefix with refactor label",
			issue:  19,
			title:  "clean up worker loops",
			labels: "refactor",
			want:   "refactor/19-clean-up-worker-loops",
		},
		{
			name:   "title without prefix with test label",
			issue:  20,
			title:  "add integration tests",
			labels: "test",
			want:   "test/20-add-integration-tests",
		},
		{
			name:   "title with uppercase prefix normalizes correctly",
			issue:  21,
			title:  "FIX: critical issue",
			labels: "",
			want:   "fix/21-critical-issue",
		},
		{
			name:   "colon in title that is not a commit type falls back to labels",
			issue:  22,
			title:  "step 1: initial setup",
			labels: "chore",
			want:   "chore/22-step-1-initial-setup",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := archieBranch(tc.issue, tc.title, "", tc.labels)
			if got != tc.want {
				t.Errorf("archieBranch(%d, %q, %q) = %q, want %q", tc.issue, tc.title, tc.labels, got, tc.want)
			}
			gotPrefix := branchPrefix(tc.title, tc.labels)
			wantPrefix := strings.Split(tc.want, "/")[0]
			if gotPrefix != wantPrefix {
				t.Errorf("branchPrefix(%q, %q) = %q, want %q", tc.title, tc.labels, gotPrefix, wantPrefix)
			}
		})
	}
}

// TestBranchSlugBoundaryAgreesWithLimit tests the 60-char slug boundary.
func TestBranchSlugBoundaryAgreesWithLimit(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		wantLen   int
		wantSlug  string
		maxCapLen int
	}{
		{
			name:      "short slug under 60 chars",
			title:     "short title",
			wantLen:   11,
			wantSlug:  "short-title",
			maxCapLen: 60,
		},
		{
			name:      "exact 60 char slug",
			title:     strings.Repeat("a", 60),
			wantLen:   60,
			wantSlug:  strings.Repeat("a", 60),
			maxCapLen: 60,
		},
		{
			name:      "slug is capped at exactly 60 chars for long title",
			title:     strings.Repeat("a", 80),
			wantLen:   60,
			wantSlug:  strings.Repeat("a", 60),
			maxCapLen: 60,
		},
		{
			name:      "long title with hyphens capped at 60",
			title:     "this is a very long title that exceeds sixty characters easily and goes on and on",
			wantLen:   60,
			wantSlug:  "this-is-a-very-long-title-that-exceeds-sixty-characters-easi",
			maxCapLen: 60,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug := branchSlug(tc.title)
			if len(slug) > tc.maxCapLen {
				t.Errorf("branchSlug(%q) length = %d, exceeds max cap %d", tc.title, len(slug), tc.maxCapLen)
			}
			if len(slug) != tc.wantLen {
				t.Errorf("branchSlug(%q) length = %d, want %d", tc.title, len(slug), tc.wantLen)
			}
			if slug != tc.wantSlug {
				t.Errorf("branchSlug(%q) = %q, want %q", tc.title, slug, tc.wantSlug)
			}
		})
	}
}
