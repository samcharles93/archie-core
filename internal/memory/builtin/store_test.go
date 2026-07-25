package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSectionName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "Facts", false},
		{"valid with spaces", "User Preferences", false},
		{"valid alphanumeric", "Section 123", false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 65), true},
		{"exactly max length", strings.Repeat("a", 64), false},
		{"invalid punctuation", "Facts!", true},
		{"invalid hyphen", "my-section", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSectionName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSectionName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func newTestStore(t *testing.T, maxBytes int) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "MEMORY.md"), maxBytes)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestStore_NewStore_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")
	s, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.Render() != "" {
		t.Fatalf("expected empty render for missing file, got %q", s.Render())
	}
	if s.MaxBytes() != defaultMaxFileBytes {
		t.Fatalf("expected default max bytes, got %d", s.MaxBytes())
	}
}

func TestStore_Add_CreatesSection(t *testing.T) {
	s := newTestStore(t, 0)

	res, err := s.Add("Facts", "the sky is blue")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !res.Created {
		t.Fatalf("expected Created=true for new section")
	}
	if res.Section != "Facts" {
		t.Fatalf("expected section Facts, got %q", res.Section)
	}

	rendered := s.Render()
	if !strings.Contains(rendered, "## Facts") {
		t.Fatalf("expected rendered content to contain header, got %q", rendered)
	}
	if !strings.Contains(rendered, "the sky is blue") {
		t.Fatalf("expected rendered content to contain the entry, got %q", rendered)
	}

	// Verify the write actually landed on disk.
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(data) != rendered {
		t.Fatalf("disk content does not match in-memory render:\ndisk: %q\nmem:  %q", data, rendered)
	}
}

func TestStore_Add_AppendsToExistingSection(t *testing.T) {
	s := newTestStore(t, 0)

	if _, err := s.Add("Facts", "first entry"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	res, err := s.Add("Facts", "second entry")
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if res.Created {
		t.Fatalf("expected Created=false when appending to existing section")
	}

	rendered := s.Render()
	if strings.Count(rendered, "## Facts") != 1 {
		t.Fatalf("expected exactly one Facts header, got: %q", rendered)
	}
	if !strings.Contains(rendered, "first entry") || !strings.Contains(rendered, "second entry") {
		t.Fatalf("expected both entries present, got %q", rendered)
	}
}

func TestStore_Add_InvalidSectionName(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("bad-name!", "content"); err == nil {
		t.Fatalf("expected error for invalid section name")
	}
}

func TestStore_Add_EmptyContent(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "   "); err == nil {
		t.Fatalf("expected error for empty content")
	}
}

func TestStore_Add_RejectsOversizedWrite(t *testing.T) {
	s := newTestStore(t, 32) // tiny bound

	_, err := s.Add("Facts", strings.Repeat("x", 100))
	if err == nil {
		t.Fatalf("expected error when write would exceed max bytes")
	}
	if s.Render() != "" {
		t.Fatalf("expected no mutation on rejected write, got %q", s.Render())
	}
	if _, statErr := os.Stat(s.Path()); statErr == nil {
		t.Fatalf("expected no file to be written on rejected write")
	}
}

func TestStore_Replace_FindsAndReplaces(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "the sky is blue"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	res, err := s.Replace("Facts", "blue", "green", true)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if res.CharsRemoved != len("blue") || res.CharsAdded != len("green") {
		t.Fatalf("unexpected char counts: %+v", res)
	}

	rendered := s.Render()
	if !strings.Contains(rendered, "the sky is green") {
		t.Fatalf("expected replacement applied, got %q", rendered)
	}
	if strings.Contains(rendered, "blue") {
		t.Fatalf("expected original substring gone, got %q", rendered)
	}
}

func TestStore_Replace_CaseInsensitive(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "The Sky Is Blue"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := s.Replace("Facts", "sky", "ocean", false); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	rendered := s.Render()
	if !strings.Contains(rendered, "The ocean Is Blue") {
		t.Fatalf("expected case-insensitive replacement, got %q", rendered)
	}
}

func TestStore_Replace_CaseSensitiveMismatchFails(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "The Sky Is Blue"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Replace("Facts", "sky", "ocean", true); err == nil {
		t.Fatalf("expected case-sensitive mismatch to fail")
	}
}

func TestStore_Replace_MissingSection(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Replace("Nope", "a", "b", true); err == nil {
		t.Fatalf("expected error for missing section")
	}
}

func TestStore_Replace_SubstringNotFound(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "the sky is blue"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Replace("Facts", "purple", "green", true); err == nil {
		t.Fatalf("expected error when substring not found")
	}
}

func TestStore_Replace_FirstOccurrenceOnly(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "cat cat cat"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Replace("Facts", "cat", "dog", true); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	rendered := s.Render()
	if !strings.Contains(rendered, "dog cat cat") {
		t.Fatalf("expected only first occurrence replaced, got %q", rendered)
	}
}

func TestStore_Remove_RemovesMatchingBlock(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "entry one"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Add("Facts", "entry two"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	res, err := s.Remove("Facts", "entry one", true)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if res.CharsRemoved != len("entry one") {
		t.Fatalf("unexpected chars removed: %d", res.CharsRemoved)
	}

	rendered := s.Render()
	if strings.Contains(rendered, "entry one") {
		t.Fatalf("expected entry one removed, got %q", rendered)
	}
	if !strings.Contains(rendered, "entry two") {
		t.Fatalf("expected entry two to remain, got %q", rendered)
	}
	// The section header should remain even though a block was removed.
	if !strings.Contains(rendered, "## Facts") {
		t.Fatalf("expected section header to remain, got %q", rendered)
	}
}

func TestStore_Remove_MissingSection(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Remove("Nope", "a", true); err == nil {
		t.Fatalf("expected error for missing section")
	}
}

func TestStore_Remove_SubstringNotFound(t *testing.T) {
	s := newTestStore(t, 0)
	if _, err := s.Add("Facts", "entry one"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Remove("Facts", "nonexistent", true); err == nil {
		t.Fatalf("expected error when substring not found")
	}
}

func TestStore_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")

	s1, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s1.Add("Facts", "persisted entry"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s2, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	if !strings.Contains(s2.Render(), "persisted entry") {
		t.Fatalf("expected reloaded store to contain persisted entry, got %q", s2.Render())
	}
}

func TestStore_Reload_PicksUpExternalChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")

	s, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.Render() != "" {
		t.Fatalf("expected empty store initially")
	}

	// Simulate an external write (e.g. a prior process, or manual edit)
	// happening after construction but before Reload.
	if err := os.WriteFile(path, []byte("## Facts\n\nexternal entry\n"), 0o644); err != nil {
		t.Fatalf("writing external content: %v", err)
	}

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !strings.Contains(s.Render(), "external entry") {
		t.Fatalf("expected reload to pick up external entry, got %q", s.Render())
	}
}

func TestStore_Reload_MissingFileResetsToEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")

	s, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s.Add("Facts", "entry"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing file: %v", err)
	}
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if s.Render() != "" {
		t.Fatalf("expected empty store after reload of missing file, got %q", s.Render())
	}
}

func TestStore_EmptySectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")

	const original = "## Empty\n\n## Next\n\nContent\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("writing seed file: %v", err)
	}

	s, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got := s.Render()
	if got != original {
		t.Fatalf("expected stable round-trip for empty section:\nwant %q\ngot  %q", original, got)
	}

	// Re-rendering after a fresh reload must be idempotent (no growth from
	// repeated parse/render cycles).
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := s.Render(); got != original {
		t.Fatalf("expected reload to preserve round-trip:\nwant %q\ngot  %q", original, got)
	}
}

func TestStore_ConsecutiveEmptySectionsRoundTrip(t *testing.T) {
	// Extends TestStore_EmptySectionRoundTrip to two consecutive empty
	// sections followed by a populated one, since the fix must generalize
	// past a single degenerate case.
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")
	multi := "## E1\n\n## E2\n\n## Facts\n\nfact one\n"
	if err := os.WriteFile(path, []byte(multi), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	s2, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := s2.Render(); got != multi {
		t.Fatalf("round-trip mismatch for consecutive empty sections:\norig:   %q\nrender: %q", multi, got)
	}
}

func TestStore_PreambleOnlyRoundTrip(t *testing.T) {
	// Regression test: a file with no section headers at all (just
	// preamble content) must not gain a spurious trailing blank line.
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")
	content := "Hello\nWorld\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	s, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := s.Render(); got != content {
		t.Fatalf("round-trip mismatch for preamble-only file:\norig:   %q\nrender: %q", content, got)
	}
}

func TestStore_MultipleSectionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")

	s1, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := s1.Add("Facts", "fact one"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s1.Add("Preferences", "pref one"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s1.Add("Facts", "fact two"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s2, err := NewStore(path, 0)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	rendered := s2.Render()
	for _, want := range []string{"## Facts", "## Preferences", "fact one", "fact two", "pref one"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered content to contain %q, got %q", want, rendered)
		}
	}

	// Now mutate the reloaded store and ensure both sections survive.
	if _, err := s2.Remove("Facts", "fact one", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	rendered = s2.Render()
	if strings.Contains(rendered, "fact one") {
		t.Fatalf("expected fact one removed, got %q", rendered)
	}
	if !strings.Contains(rendered, "fact two") || !strings.Contains(rendered, "pref one") {
		t.Fatalf("expected other entries to survive, got %q", rendered)
	}
}
