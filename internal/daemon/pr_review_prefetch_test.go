package daemon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/infrastructure/prsource"
)

// buildTestArchive builds a gzipped tar containing files, each wrapped under
// a synthetic top-level directory the way a forge's repo-archive endpoint
// does, so prefetchPRReview exercises the same extraction path production
// traffic does.
func buildTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: "repo-abc123/" + name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakePRForge struct {
	pr        forge.PullRequest
	diff      string
	archive   []byte
	pullErr   error
	diffErr   error
	archErr   error
	archCalls int
}

func (f *fakePRForge) GetPullRequest(context.Context, string, string, int) (forge.PullRequest, error) {
	return f.pr, f.pullErr
}

func (f *fakePRForge) GetPullRequestDiff(context.Context, string, string, int) (string, error) {
	return f.diff, f.diffErr
}

func (f *fakePRForge) GetRepoArchive(context.Context, string, string, string) (io.ReadCloser, error) {
	f.archCalls++
	if f.archErr != nil {
		return nil, f.archErr
	}
	return io.NopCloser(strings.NewReader(string(f.archive))), nil
}

func TestPrefetchPRReviewWritesMetadataDiffAndSnapshot(t *testing.T) {
	archive := buildTestArchive(t, map[string]string{"a.go": "package a\n"})
	f := &fakePRForge{
		pr:      forge.PullRequest{Title: "t", Body: "b", HeadSHA: "abc123"},
		diff:    "diff --git a/x b/x\n",
		archive: archive,
	}
	workDir := t.TempDir()

	if err := prefetchPRReview(context.Background(), f, "acme", "widget", 7, workDir); err != nil {
		t.Fatalf("prefetchPRReview: %v", err)
	}

	base := filepath.Join(workDir, prsource.PrefetchDirName)
	meta, err := os.ReadFile(filepath.Join(base, "metadata.json"))
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	if !strings.Contains(string(meta), "abc123") || !strings.Contains(string(meta), `"t"`) {
		t.Fatalf("metadata.json = %s, want title and head_sha", meta)
	}
	diff, err := os.ReadFile(filepath.Join(base, "diff.patch"))
	if err != nil {
		t.Fatalf("read diff.patch: %v", err)
	}
	if string(diff) != "diff --git a/x b/x\n" {
		t.Fatalf("diff.patch = %q", diff)
	}
	if _, err := os.Stat(filepath.Join(base, "snapshot", "a.go")); err != nil {
		t.Fatalf("snapshot not extracted: %v", err)
	}
}

func TestPrefetchPRReviewFailsWhenForgeCannotReadPullRequests(t *testing.T) {
	workDir := t.TempDir()
	if err := prefetchPRReview(context.Background(), noPRCapability{}, "acme", "widget", 7, workDir); err == nil {
		t.Fatal("want an error when the forge cannot read pull requests")
	}
}

func TestPrefetchPRReviewPropagatesForgeError(t *testing.T) {
	f := &fakePRForge{pullErr: errors.New("boom")}
	workDir := t.TempDir()
	if err := prefetchPRReview(context.Background(), f, "acme", "widget", 7, workDir); err == nil {
		t.Fatal("want the forge error surfaced")
	}
}

// noPRCapability satisfies forge.Forge's method set with none of
// PullRequestReader/PullRequestDiffReader/RepoArchiveReader -- like the noop
// forge, or a forge configured without PR-reading support.
type noPRCapability struct{}
