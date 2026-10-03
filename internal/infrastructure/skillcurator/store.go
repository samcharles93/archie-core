// Package skillcurator implements domain/curator.CuratorEngine for skill
// maintenance.
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
// name and value -- the discovery convention is fixed
// across both packages, not configurable per caller.
const skillsDir = ".agents/skills"

// skillFile is the file name every skill's definition lives in, within
// its own directory under skillsDir.
const skillFile = "SKILL.md"

// Store implements curator.SkillStore over root's */SKILL.md files.
type Store struct {
	root string
}

// NewStore builds a Store rooted at root.
func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) skillsPath() string {
	return filepath.Join(s.root, skillsDir)
}

func (s *Store) skillPath(name string) string {
	return filepath.Join(s.skillsPath(), name, skillFile)
}

// List returns every skill directory with a SKILL.md. A missing directory
// returns an empty list.
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

// Read returns the skill's raw SKILL.md. Description is empty when the
// frontmatter does not parse.
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

// Write overwrites an existing skill's SKILL.md.
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
