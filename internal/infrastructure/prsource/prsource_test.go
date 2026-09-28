package prsource

import (
	"context"
	"errors"
	"testing"
)

type fakeForge struct {
	pr  PullRequest
	err error
}

func (f *fakeForge) GetPullRequest(context.Context, string, string, int) (PullRequest, error) {
	return f.pr, f.err
}

type fakeTrees struct {
	dir  string
	diff string
	err  error
}

func (f *fakeTrees) CheckoutPR(context.Context, string, string, string, string) (string, func(), error) {
	if f.err != nil {
		return "", func() {}, f.err
	}
	return f.dir, func() {}, nil
}

func (f *fakeTrees) Diff(context.Context, string, string) (string, error) {
	return f.diff, f.err
}

func (f *fakeTrees) Snapshot(context.Context, string, string) error {
	return f.err
}

func TestMetadataMapsForgePullRequest(t *testing.T) {
	forge := &fakeForge{pr: PullRequest{Title: "t", Body: "b", HeadSHA: "abc"}}
	s := New(forge, &fakeTrees{})

	meta, err := s.Metadata(context.Background(), "acme", "widgets", 7)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Title != "t" || meta.Body != "b" {
		t.Errorf("meta = %+v, want title/body from the forge PR", meta)
	}
}

func TestMetadataPropagatesForgeError(t *testing.T) {
	wantErr := errors.New("forge unreachable")
	s := New(&fakeForge{err: wantErr}, &fakeTrees{})

	if _, err := s.Metadata(context.Background(), "acme", "widgets", 7); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestDiffChecksOutThePRAndDiffsAgainstBase(t *testing.T) {
	forge := &fakeForge{pr: PullRequest{HeadRef: "feature", BaseRef: "main"}}
	trees := &fakeTrees{dir: "/tmp/checkout", diff: "diff --git a b"}
	s := New(forge, trees)

	diff, err := s.Diff(context.Background(), "acme", "widgets", 7)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "diff --git a b" {
		t.Errorf("diff = %q, want the checked-out worktree's diff", diff)
	}
}

func TestSnapshotReturnsTheHeadSHAForgeReported(t *testing.T) {
	forge := &fakeForge{pr: PullRequest{HeadSHA: "deadbeef"}}
	trees := &fakeTrees{dir: "/tmp/checkout"}
	s := New(forge, trees)

	headSHA, err := s.Snapshot(context.Background(), "acme", "widgets", 7, t.TempDir())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if headSHA != "deadbeef" {
		t.Errorf("headSHA = %q, want the forge-reported head SHA", headSHA)
	}
}

func TestSnapshotPropagatesCheckoutFailure(t *testing.T) {
	wantErr := errors.New("clone failed")
	s := New(&fakeForge{}, &fakeTrees{err: wantErr})

	if _, err := s.Snapshot(context.Background(), "acme", "widgets", 7, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
