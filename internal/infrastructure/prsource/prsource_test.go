package prsource

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type fakeForge struct {
	pr      PullRequest
	diff    string
	archive []byte
	err     error
}

func (f *fakeForge) GetPullRequest(context.Context, string, string, int) (PullRequest, error) {
	return f.pr, f.err
}

func (f *fakeForge) GetPullRequestDiff(context.Context, string, string, int) (string, error) {
	return f.diff, f.err
}

func (f *fakeForge) GetRepoArchive(context.Context, string, string, string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.archive)), nil
}

func TestMetadataMapsForgePullRequest(t *testing.T) {
	forge := &fakeForge{pr: PullRequest{Title: "t", Body: "b", HeadSHA: "abc"}}
	s := New(forge)

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
	s := New(&fakeForge{err: wantErr})

	if _, err := s.Metadata(context.Background(), "acme", "widgets", 7); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestDiffFetchesOverHTTPWithNoCheckout(t *testing.T) {
	forge := &fakeForge{diff: "diff --git a b"}
	s := New(forge)

	diff, err := s.Diff(context.Background(), "acme", "widgets", 7)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "diff --git a b" {
		t.Errorf("diff = %q, want the forge's raw diff response", diff)
	}
}

func TestDiffPropagatesForgeError(t *testing.T) {
	wantErr := errors.New("diff fetch failed")
	s := New(&fakeForge{err: wantErr})

	if _, err := s.Diff(context.Background(), "acme", "widgets", 7); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestSnapshotExtractsTheArchiveAndReturnsTheHeadSHA(t *testing.T) {
	archive := buildTarGz(t, "acme-widgets-deadbeef", map[string]string{"main.go": "package main\n"})
	forge := &fakeForge{pr: PullRequest{HeadSHA: "deadbeef"}, archive: archive}
	s := New(forge)
	dest := t.TempDir()

	headSHA, err := s.Snapshot(context.Background(), "acme", "widgets", 7, dest)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if headSHA != "deadbeef" {
		t.Errorf("headSHA = %q, want the forge-reported head SHA", headSHA)
	}
	if _, err := os.Stat(filepath.Join(dest, "main.go")); err != nil {
		t.Errorf("expected the extracted file to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "acme-widgets-deadbeef")); err == nil {
		t.Error("the wrapper directory itself should not survive extraction")
	}
}

func TestSnapshotPropagatesForgeError(t *testing.T) {
	wantErr := errors.New("archive fetch failed")
	s := New(&fakeForge{err: wantErr})

	if _, err := s.Snapshot(context.Background(), "acme", "widgets", 7, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
