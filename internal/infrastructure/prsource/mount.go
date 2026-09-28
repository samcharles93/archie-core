package prsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// PrefetchDirName is the subdirectory of a task's worktree directory the
// daemon's prefetch step writes pull request data into, and MountSource
// reads it back from. Named once here so the daemon-side writer and this
// package's reader cannot drift into two different paths.
const PrefetchDirName = ".archie-pr-review"

// MountSource implements workflow.PRSource by reading data the daemon
// already fetched and wrote to disk before the container started, instead of
// calling the forge itself. This is how the sandboxed pipeline gets pull
// request data with no forge credential ever entering the container: the
// daemon (which holds the credential) runs Source (this package's
// HTTP-backed implementation) against baseDir before handing the container
// off, and the pipeline's Stage.Run bodies read the result through
// MountSource instead.
//
// baseDir holds three artifacts, all written by prefetchTo (the daemon-side
// counterpart, in internal/daemon):
//
//	metadata.json  -- {"title", "body", "head_sha"}
//	diff.patch     -- the raw unified diff
//	snapshot/      -- the extracted repository tree at head_sha
type MountSource struct {
	baseDir string
}

var _ workflow.PRSource = (*MountSource)(nil)

// NewFromMount builds a MountSource reading from baseDir.
func NewFromMount(baseDir string) *MountSource {
	return &MountSource{baseDir: baseDir}
}

type mountMetadata struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	HeadSHA string `json:"head_sha"`
}

func (s *MountSource) readMetadata() (mountMetadata, error) {
	data, err := os.ReadFile(filepath.Join(s.baseDir, "metadata.json"))
	if err != nil {
		return mountMetadata{}, fmt.Errorf("prsource: read prefetched metadata: %w", err)
	}
	var m mountMetadata
	if err := json.Unmarshal(data, &m); err != nil {
		return mountMetadata{}, fmt.Errorf("prsource: decode prefetched metadata: %w", err)
	}
	return m, nil
}

// Metadata reads the prefetched title/body. owner, repo and number are
// unused: baseDir already names exactly one pull request, the one the
// daemon prefetched for this task.
func (s *MountSource) Metadata(_ context.Context, _, _ string, _ int) (workflow.PRMetadata, error) {
	m, err := s.readMetadata()
	if err != nil {
		return workflow.PRMetadata{}, err
	}
	return workflow.PRMetadata{Title: m.Title, Body: m.Body}, nil
}

// Diff reads the prefetched unified diff.
func (s *MountSource) Diff(_ context.Context, _, _ string, _ int) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.baseDir, "diff.patch"))
	if err != nil {
		return "", fmt.Errorf("prsource: read prefetched diff: %w", err)
	}
	return string(data), nil
}

// Snapshot copies the prefetched snapshot tree into destDir and returns the
// head SHA it was measured against. This is a local filesystem copy inside
// the same container -- no network call, no credential -- since the daemon
// already extracted the archive into baseDir/snapshot before the container
// started.
func (s *MountSource) Snapshot(_ context.Context, _, _ string, _ int, destDir string) (string, error) {
	m, err := s.readMetadata()
	if err != nil {
		return "", err
	}
	if err := copyTree(filepath.Join(s.baseDir, "snapshot"), destDir); err != nil {
		return "", fmt.Errorf("prsource: copy prefetched snapshot: %w", err)
	}
	return m.HeadSHA, nil
}

// copyTree copies every file under src into dst, preserving the relative
// directory structure. dst is expected to already exist.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
