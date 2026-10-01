// Not lifted from tau: archie-original. It gives the lifted write and edit
// tools something tau never had -- a pre-mutation checkpoint -- and stores it
// in the per-workspace state directory those tools already run against.
package builtin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// checkpointDir is the workspace-relative directory a task's pre-mutation
// file checkpoints live under. It sits inside .git for the same reason the
// task brief (container.WriteTaskJSON) and the prepared sentinel
// (worktree.preparedSentinel) do: the workspace is a git worktree the agent
// commits from, and go-git's Add{All:true} honours neither .gitignore nor
// .git/info/exclude, so anything outside .git can be swept onto the task
// branch. Nothing under .git can ever be tracked.
const checkpointDir = ".git/archie-checkpoints"

// Checkpoint is a file's pre-mutation state: its bytes, or the fact that it
// did not exist.
type Checkpoint struct {
	// ID identifies this checkpoint and names its file on disk.
	ID string `json:"id"`
	// Path is the absolute path the snapshot was taken from.
	Path string `json:"path"`
	// Existed reports whether the file was present. False means restore
	// removes Path.
	Existed bool `json:"existed"`
	// Mode is the file's permission bits when it existed.
	Mode os.FileMode `json:"mode"`
	// Content is the file's bytes when it existed.
	Content []byte `json:"content,omitempty"`
	// CreatedAt orders checkpoints for RestoreLatest.
	CreatedAt time.Time `json:"created_at"`
}

// CheckpointStore keeps pre-mutation file checkpoints for one workspace. It
// is written by the mutating built-in tools (write, edit) and read by a
// rollback caller; it is not itself a tool.
type CheckpointStore struct {
	root string
	now  func() time.Time
}

// NewCheckpointStore returns the store rooted at workspace's per-workspace
// state directory. workspace is the same directory the file tools are rooted
// at, so the store travels with the workspace it protects.
func NewCheckpointStore(workspace string) *CheckpointStore {
	return &CheckpointStore{
		root: filepath.Join(workspace, filepath.FromSlash(checkpointDir)),
		now:  time.Now,
	}
}

// Snapshot records path's current state and returns the checkpoint. It must
// be called before the mutation it guards: an error means the mutation must
// not proceed, because the pre-mutation state could not be preserved.
//
// A missing file is a valid snapshot (Existed=false) so that creating a file
// is restorable too. Non-regular files are refused: a checkpoint can restore
// bytes, not a socket, device or directory.
func (s *CheckpointStore) Snapshot(path string) (Checkpoint, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint %s: resolve path: %w", path, err)
	}

	cp := Checkpoint{Path: abs, CreatedAt: s.now()}

	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// New file: the checkpoint records absence so restore removes it.
	case err != nil:
		return Checkpoint{}, fmt.Errorf("checkpoint %s: stat: %w", abs, err)
	case info.IsDir() || !info.Mode().IsRegular():
		return Checkpoint{}, fmt.Errorf("checkpoint %s: refusing to checkpoint non-regular file (%s)", abs, info.Mode())
	default:
		content, readErr := os.ReadFile(abs)
		if readErr != nil {
			return Checkpoint{}, fmt.Errorf("checkpoint %s: read: %w", abs, readErr)
		}
		cp.Existed = true
		cp.Mode = info.Mode().Perm()
		cp.Content = content
	}

	sum := sha256.Sum256(append([]byte(abs+"\x00"), cp.Content...))
	cp.ID = fmt.Sprintf("%019d-%s", cp.CreatedAt.UnixNano(), hex.EncodeToString(sum[:6]))

	if err := s.write(cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// write persists one checkpoint atomically.
func (s *CheckpointStore) write(cp Checkpoint) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("checkpoint %s: create store %s: %w", cp.Path, s.root, err)
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("checkpoint %s: encode: %w", cp.Path, err)
	}
	if err := writeFileAtomic(filepath.Join(s.root, cp.ID+".json"), data, 0o600); err != nil {
		return fmt.Errorf("checkpoint %s: persist: %w", cp.Path, err)
	}
	return nil
}

// RestoreLatest puts path back to its most recently taken checkpoint and
// returns that checkpoint. A path with no checkpoint is an error, never a
// silent no-op: a rollback that quietly does nothing is worse than one that
// says it could not.
func (s *CheckpointStore) RestoreLatest(path string) (Checkpoint, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("restore %s: resolve path: %w", path, err)
	}
	cp, ok, err := s.latest(abs)
	if err != nil {
		return Checkpoint{}, err
	}
	if !ok {
		return Checkpoint{}, fmt.Errorf("restore %s: no checkpoint recorded", abs)
	}
	if err := s.restore(cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// latest returns the newest checkpoint recorded for abs.
func (s *CheckpointStore) latest(abs string) (Checkpoint, bool, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("restore %s: read store: %w", abs, err)
	}

	var matches []Checkpoint
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if readErr != nil {
			return Checkpoint{}, false, fmt.Errorf("restore %s: read checkpoint %s: %w", abs, entry.Name(), readErr)
		}
		var cp Checkpoint
		if jsonErr := json.Unmarshal(data, &cp); jsonErr != nil {
			return Checkpoint{}, false, fmt.Errorf("restore %s: decode checkpoint %s: %w", abs, entry.Name(), jsonErr)
		}
		if cp.Path == abs {
			matches = append(matches, cp)
		}
	}
	if len(matches) == 0 {
		return Checkpoint{}, false, nil
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].ID > matches[j].ID
		}
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	return matches[0], true, nil
}

// restore writes a checkpoint's state back to its path.
func (s *CheckpointStore) restore(cp Checkpoint) error {
	if !cp.Existed {
		if err := os.Remove(cp.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restore %s: remove: %w", cp.Path, err)
		}
		return nil
	}
	mode := cp.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := writeFileAtomic(cp.Path, cp.Content, mode); err != nil {
		return fmt.Errorf("restore %s: write: %w", cp.Path, err)
	}
	return nil
}
