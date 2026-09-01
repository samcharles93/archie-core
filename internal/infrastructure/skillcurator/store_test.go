package skillcurator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/curator"
)

func writeSkill(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) = %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillFile), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile = %v", err)
	}
}

const validSkill = "---\nname: tdd-bugfix\ndescription: Fix a bug with a red-then-green test\n---\nBody text.\n"

func TestStoreListMissingDirIsEmptyNotError(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	refs, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	if len(refs) != 0 {
		t.Fatalf("List() = %v, want empty", refs)
	}
}

func TestStoreListSkipsADirectoryWithNoSkillFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "good", validSkill)
	if err := os.MkdirAll(filepath.Join(root, skillsDir, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewStore(root)
	refs, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List() = %v, want nil", err)
	}
	if len(refs) != 1 || refs[0].Name != "good" {
		t.Fatalf("List() = %v, want only 'good'", refs)
	}
}

func TestStoreReadReturnsContentAndDescription(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "tdd-bugfix", validSkill)

	s := NewStore(root)
	sk, err := s.Read(context.Background(), "tdd-bugfix")
	if err != nil {
		t.Fatalf("Read() = %v, want nil", err)
	}
	if sk.Content != validSkill {
		t.Errorf("Content = %q, want the raw file content", sk.Content)
	}
	if sk.Description != "Fix a bug with a red-then-green test" {
		t.Errorf("Description = %q, want the frontmatter description", sk.Description)
	}
}

func TestStoreReadUnparseableSkillStillReturnsRawContent(t *testing.T) {
	// Read never treats a parse failure as an error -- Pass decides that,
	// via its own skill.Parse call. Description is simply empty.
	t.Parallel()
	root := t.TempDir()
	const broken = "---\nname: [unterminated\n---\n"
	writeSkill(t, root, "broken", broken)

	s := NewStore(root)
	sk, err := s.Read(context.Background(), "broken")
	if err != nil {
		t.Fatalf("Read() = %v, want nil even for unparseable frontmatter", err)
	}
	if sk.Content != broken {
		t.Errorf("Content = %q, want the raw content regardless of parse failure", sk.Content)
	}
	if sk.Description != "" {
		t.Errorf("Description = %q, want empty for a skill that failed to parse", sk.Description)
	}
}

func TestStoreReadMissingSkillIsAnError(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	if _, err := s.Read(context.Background(), "nonexistent"); err == nil {
		t.Fatal("Read(nonexistent) = nil, want error")
	}
}

func TestStoreWriteRequiresAnExistingSkillDirectory(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	err := s.Write(context.Background(), curator.Skill{Name: "new-skill", Content: "content"})
	if err == nil {
		t.Fatal("Write(new skill) = nil, want error: Write curates, it does not create")
	}
}

func TestStoreWritePersistsContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "tdd-bugfix", validSkill)

	s := NewStore(root)
	updated := validSkill + "\nMore body.\n"
	if err := s.Write(context.Background(), curator.Skill{Name: "tdd-bugfix", Content: updated}); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	got, err := s.Read(context.Background(), "tdd-bugfix")
	if err != nil {
		t.Fatalf("Read() after Write = %v", err)
	}
	if got.Content != updated {
		t.Errorf("Content after Write = %q, want %q", got.Content, updated)
	}
}

func TestStoreDeleteRemovesTheWholeSkillDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeSkill(t, root, "tdd-bugfix", validSkill)
	if err := os.WriteFile(filepath.Join(root, skillsDir, "tdd-bugfix", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(root)
	if err := s.Delete(context.Background(), "tdd-bugfix"); err != nil {
		t.Fatalf("Delete() = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(root, skillsDir, "tdd-bugfix")); !os.IsNotExist(err) {
		t.Fatalf("skill directory still exists after Delete, or unexpected error: %v", err)
	}
}

func TestStoreDeleteMissingSkillIsNotAnError(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Fatalf("Delete(missing) = %v, want nil: the end state (absent) already holds", err)
	}
}
