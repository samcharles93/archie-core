// Package skillcurator implements domain/curator.CuratorEngine for skill
// maintenance. See docs/prds/skill-curator.md for what a pass does and
// why, and the scope this package deliberately does not cover.
package skillcurator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/skill"
)

// skillsDir mirrors internal/skill's own unexported constant of the same
// name and value -- the discovery convention (.agents/skills/) is fixed
// across both packages, not configurable per caller.
const skillsDir = ".agents/skills"

// skillFile is the file name every skill's definition lives in, within
// its own directory under skillsDir.
const skillFile = "SKILL.md"

// Store implements curator.SkillStore over one root directory's
// .agents/skills/*/SKILL.md files. Unlike internal/skill.Discover, which
// parses every skill up front and fails the whole call if any one of
// them doesn't parse, Store's List and Read never parse frontmatter
// themselves beyond a best-effort Description -- a per-skill parse
// failure is the curator's problem to report as an Action, not this
// store's problem to abort on. See docs/prds/skill-curator.md.
type Store struct {
	root string
}

// NewStore builds a Store rooted at root (root/.agents/skills/...).
func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) skillsPath() string {
	return filepath.Join(s.root, skillsDir)
}

func (s *Store) skillPath(name string) string {
	return filepath.Join(s.skillsPath(), name, skillFile)
}

// List returns every skill directory under root, in directory order. A
// directory with no SKILL.md is skipped -- consistent with
// internal/skill.Discover's own treatment of that case -- and a missing
// skills directory returns an empty list, not an error, matching
// internal/skill's discovery convention (missing directory is not an
// error).
func (s *Store) List(_ context.Context) ([]curator.SkillRef, error) {
	entries, err := os.ReadDir(s.skillsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skillcurator: reading %s: %w", s.skillsPath(), err)
	}

	var refs []curator.SkillRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := s.skillPath(e.Name())
		if _, err := os.Stat(path); err != nil {
			continue
		}
		refs = append(refs, curator.SkillRef{Name: e.Name(), Path: path})
	}
	return refs, nil
}

// Read returns the named skill's raw SKILL.md content. Description is a
// best-effort frontmatter read: empty on a parse failure, which Read
// does not treat as an error -- Pass is what decides a parse failure is
// worth reporting, and it needs the raw Content to do so via its own
// skill.Parse call regardless of whether this convenience field could be
// populated.
func (s *Store) Read(_ context.Context, name string) (curator.Skill, error) {
	path := s.skillPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return curator.Skill{}, fmt.Errorf("skillcurator: reading %s: %w", path, err)
	}
	sk := curator.Skill{Name: name, Content: string(data)}
	if fm, _, err := skill.Parse(data); err == nil {
		sk.Description = fm.Description
	}
	return sk, nil
}

// Write persists s.Content to the named skill's SKILL.md. The skill's
// directory must already exist -- Write curates an existing skill, it
// does not create a new one, which is a bigger decision (naming,
// directory layout, an author writing the rest of the frontmatter) that
// this store does not make.
func (s *Store) Write(_ context.Context, sk curator.Skill) error {
	dir := filepath.Join(s.skillsPath(), sk.Name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("skillcurator: skill %q does not exist under %s", sk.Name, s.skillsPath())
	}
	if err := os.WriteFile(s.skillPath(sk.Name), []byte(sk.Content), 0o644); err != nil {
		return fmt.Errorf("skillcurator: writing %s: %w", s.skillPath(sk.Name), err)
	}
	return nil
}

// Delete removes the named skill's entire directory (SKILL.md, plugins/,
// anything else under it) -- not just SKILL.md, matching "delete a
// skill." Deleting a name that does not exist is not an error: the end
// state (skill absent) already holds.
func (s *Store) Delete(_ context.Context, name string) error {
	dir := filepath.Join(s.skillsPath(), name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("skillcurator: deleting %s: %w", dir, err)
	}
	return nil
}
