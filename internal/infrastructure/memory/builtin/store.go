// Package builtin implements the built-in, file-backed MemoryProvider. It
// persists agent memory to MEMORY.md and USER.md as plain markdown, using
// "## " headers to delimit named sections. Content within a section is
// organized into blank-line-separated blocks; add/replace/remove act on
// those blocks and on substrings within them.
package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// defaultMaxFileBytes is the default per-file size bound (100KB).
const defaultMaxFileBytes = 100 * 1024

// sectionNamePattern restricts section names to alphanumerics and spaces,
// matching the tool's input-validation contract.
var sectionNamePattern = regexp.MustCompile(`^[A-Za-z0-9 ]{1,64}$`)

// ValidateSectionName reports an error if name is empty, too long, or
// contains characters outside [A-Za-z0-9 ].
func ValidateSectionName(name string) error {
	if !sectionNamePattern.MatchString(name) {
		return fmt.Errorf("builtin: invalid section name %q: must be 1-64 alphanumeric/space characters", name)
	}
	return nil
}

// section is a named markdown block within a store file. blocks holds the
// section's content split into blank-line-separated paragraphs.
type section struct {
	name   string
	blocks []string
}

// clone returns a deep copy of the section so callers can mutate a
// candidate version without touching the committed state.
func (s *section) clone() *section {
	blocks := make([]string, len(s.blocks))
	copy(blocks, s.blocks)
	return &section{name: s.name, blocks: blocks}
}

// Store is a thread-safe, size-bounded, file-backed markdown document. It
// reads its full content at construction and rewrites the whole file on
// every mutation.
type Store struct {
	mu       sync.RWMutex
	path     string
	maxBytes int

	preamble string
	sections []*section
}

// NewStore loads path if it exists (creating an empty in-memory document
// otherwise) and returns a Store bounded to maxBytes. maxBytes <= 0 uses
// defaultMaxFileBytes.
func NewStore(path string, maxBytes int) (*Store, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxFileBytes
	}
	s := &Store{path: path, maxBytes: maxBytes}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("builtin: reading %s: %w", path, err)
	}
	s.preamble, s.sections = parseSections(string(data))
	return s, nil
}

// emptyStore returns a Store bound to path with no content, for a file that
// could not be read. Reads answer as empty and a later write still attempts
// the file, so a directory that becomes usable repairs itself.
func emptyStore(path string, maxBytes int) *Store {
	if maxBytes <= 0 {
		maxBytes = defaultMaxFileBytes
	}
	return &Store{path: path, maxBytes: maxBytes}
}

// Reload re-reads the backing file from disk, replacing all in-memory
// state. A missing file resets the store to empty rather than erroring.
func (s *Store) Reload() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.mu.Lock()
			s.preamble, s.sections = "", nil
			s.mu.Unlock()
			return nil
		}
		return fmt.Errorf("builtin: reading %s: %w", s.path, err)
	}

	preamble, sections := parseSections(string(data))
	s.mu.Lock()
	s.preamble, s.sections = preamble, sections
	s.mu.Unlock()
	return nil
}

// Path returns the file path backing this store.
func (s *Store) Path() string {
	return s.path
}

// MaxBytes returns the configured size bound for this store.
func (s *Store) MaxBytes() int {
	return s.maxBytes
}

// Render returns the current serialized document. Safe for concurrent use.
func (s *Store) Render() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return renderSections(s.preamble, s.sections)
}

// Size returns the current serialized document length in bytes.
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(renderSections(s.preamble, s.sections))
}

// AddResult describes the outcome of a successful Add.
type AddResult struct {
	Section      string
	CharsAdded   int
	SectionChars int
	FileBytes    int
	Created      bool // true if the section did not exist before this call
}

// Add appends content as a new block in the named section, creating the
// section if it does not already exist. The write is rejected  --  with no
// change to in-memory state or disk  --  if the resulting file would exceed
// the store's size bound.
func (s *Store) Add(sectionName, content string) (AddResult, error) {
	if err := ValidateSectionName(sectionName); err != nil {
		return AddResult{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return AddResult{}, fmt.Errorf("builtin: content must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	candidate := cloneSections(s.sections)
	sec, created := findOrCreateSection(&candidate, sectionName)
	sec.blocks = append(sec.blocks, content)

	rendered := renderSections(s.preamble, candidate)
	if len(rendered) > s.maxBytes {
		return AddResult{}, fmt.Errorf("builtin: add to section %q would exceed max file size (%d > %d bytes)", sectionName, len(rendered), s.maxBytes)
	}
	if err := writeFileAtomic(s.path, rendered); err != nil {
		return AddResult{}, err
	}

	s.sections = candidate
	return AddResult{
		Section:      sectionName,
		CharsAdded:   len(content),
		SectionChars: sectionChars(sec),
		FileBytes:    len(rendered),
		Created:      created,
	}, nil
}

// ReplaceResult describes the outcome of a successful Replace.
type ReplaceResult struct {
	Section      string
	CharsRemoved int
	CharsAdded   int
	SectionChars int
	FileBytes    int
}

// Replace finds the first occurrence of substring within the named
// section's content and replaces it with replacement. It returns an error
// if the section does not exist, if substring is not found, or if the
// result would exceed the store's size bound.
func (s *Store) Replace(sectionName, substring, replacement string, caseSensitive bool) (ReplaceResult, error) {
	if err := ValidateSectionName(sectionName); err != nil {
		return ReplaceResult{}, err
	}
	if substring == "" {
		return ReplaceResult{}, fmt.Errorf("builtin: substring must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	candidate := cloneSections(s.sections)
	sec := findSection(candidate, sectionName)
	if sec == nil {
		return ReplaceResult{}, fmt.Errorf("builtin: section %q not found", sectionName)
	}

	blockIdx, pos, matchLen := findInBlocks(sec.blocks, substring, caseSensitive)
	if blockIdx < 0 {
		return ReplaceResult{}, fmt.Errorf("builtin: substring %q not found in section %q", substring, sectionName)
	}

	block := sec.blocks[blockIdx]
	sec.blocks[blockIdx] = block[:pos] + replacement + block[pos+matchLen:]

	rendered := renderSections(s.preamble, candidate)
	if len(rendered) > s.maxBytes {
		return ReplaceResult{}, fmt.Errorf("builtin: replace in section %q would exceed max file size (%d > %d bytes)", sectionName, len(rendered), s.maxBytes)
	}
	if err := writeFileAtomic(s.path, rendered); err != nil {
		return ReplaceResult{}, err
	}

	s.sections = candidate
	return ReplaceResult{
		Section:      sectionName,
		CharsRemoved: matchLen,
		CharsAdded:   len(replacement),
		SectionChars: sectionChars(sec),
		FileBytes:    len(rendered),
	}, nil
}

// RemoveResult describes the outcome of a successful Remove.
type RemoveResult struct {
	Section      string
	CharsRemoved int
	SectionChars int
	FileBytes    int
}

// Remove deletes the first block within the named section whose text
// contains substring. It returns an error if the section does not exist
// or if no block matches.
func (s *Store) Remove(sectionName, substring string, caseSensitive bool) (RemoveResult, error) {
	if err := ValidateSectionName(sectionName); err != nil {
		return RemoveResult{}, err
	}
	if substring == "" {
		return RemoveResult{}, fmt.Errorf("builtin: substring must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	candidate := cloneSections(s.sections)
	sec := findSection(candidate, sectionName)
	if sec == nil {
		return RemoveResult{}, fmt.Errorf("builtin: section %q not found", sectionName)
	}

	blockIdx, _, _ := findInBlocks(sec.blocks, substring, caseSensitive)
	if blockIdx < 0 {
		return RemoveResult{}, fmt.Errorf("builtin: substring %q not found in section %q", substring, sectionName)
	}

	removed := sec.blocks[blockIdx]
	sec.blocks = append(sec.blocks[:blockIdx], sec.blocks[blockIdx+1:]...)

	rendered := renderSections(s.preamble, candidate)
	// Removal only ever shrinks the file, but a bound check is kept for
	// consistency with Add/Replace and to guard against pathological
	// preamble/header growth from re-serialization.
	if len(rendered) > s.maxBytes {
		return RemoveResult{}, fmt.Errorf("builtin: remove from section %q would exceed max file size (%d > %d bytes)", sectionName, len(rendered), s.maxBytes)
	}
	if err := writeFileAtomic(s.path, rendered); err != nil {
		return RemoveResult{}, err
	}

	s.sections = candidate
	return RemoveResult{
		Section:      sectionName,
		CharsRemoved: len(removed),
		SectionChars: sectionChars(sec),
		FileBytes:    len(rendered),
	}, nil
}

// ── Parsing & rendering ────────────────────────────────────────────────

// sectionHeaderPrefix is the markdown header marker that delimits sections.
const sectionHeaderPrefix = "## "

// parseSections splits document content into a preamble (any content before
// the first section header) and an ordered list of sections. Each section's
// body is split into blocks on blank lines.
func parseSections(content string) (preamble string, sections []*section) {
	lines := strings.Split(content, "\n")

	var preambleLines []string
	var cur *section
	var bodyLines []string

	flush := func() {
		if cur != nil {
			cur.blocks = splitBlocks(bodyLines)
			sections = append(sections, cur)
		}
		bodyLines = nil
	}

	for _, line := range lines {
		if strings.HasPrefix(line, sectionHeaderPrefix) {
			flush()
			name := strings.TrimSpace(strings.TrimPrefix(line, sectionHeaderPrefix))
			cur = &section{name: name}
			continue
		}
		if cur == nil {
			preambleLines = append(preambleLines, line)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()

	preamble = strings.TrimRight(strings.Join(preambleLines, "\n"), "\n \t")
	return preamble, sections
}

// splitBlocks groups lines into blank-line-separated blocks, trimming
// leading/trailing blank lines and skipping empty blocks.
func splitBlocks(lines []string) []string {
	var blocks []string
	var cur []string

	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.TrimRight(strings.Join(cur, "\n"), "\n"))
			cur = nil
		}
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
}

// renderSections serializes a preamble and ordered sections back into the
// on-disk markdown format.
func renderSections(preamble string, sections []*section) string {
	var sb strings.Builder
	if preamble != "" {
		sb.WriteString(preamble)
		if len(sections) > 0 {
			sb.WriteString("\n\n")
		} else {
			sb.WriteString("\n")
		}
	}
	for i, sec := range sections {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(sectionHeaderPrefix)
		sb.WriteString(sec.name)
		sb.WriteString("\n")
		if len(sec.blocks) > 0 {
			sb.WriteString("\n")
			for j, block := range sec.blocks {
				if j > 0 {
					sb.WriteString("\n\n")
				}
				sb.WriteString(block)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// sectionChars returns the serialized character count of a single section's
// body (blocks only, joined as they would appear on disk).
func sectionChars(sec *section) int {
	return len(strings.Join(sec.blocks, "\n\n"))
}

// cloneSections returns a deep copy of a section slice.
func cloneSections(sections []*section) []*section {
	out := make([]*section, len(sections))
	for i, sec := range sections {
		out[i] = sec.clone()
	}
	return out
}

// findSection returns the section with the given name, or nil.
func findSection(sections []*section, name string) *section {
	for _, sec := range sections {
		if sec.name == name {
			return sec
		}
	}
	return nil
}

// findOrCreateSection returns the named section from *sections, appending a
// new empty section (and reporting created=true) if none exists.
func findOrCreateSection(sections *[]*section, name string) (sec *section, created bool) {
	if sec := findSection(*sections, name); sec != nil {
		return sec, false
	}
	sec = &section{name: name}
	*sections = append(*sections, sec)
	return sec, true
}

// findInBlocks searches blocks in order for the first occurrence of
// substring, returning the block index, the byte offset within that block,
// and the byte length of the match. Returns (-1, -1, 0) if not found.
func findInBlocks(blocks []string, substring string, caseSensitive bool) (blockIdx, pos, matchLen int) {
	for i, block := range blocks {
		if caseSensitive {
			if idx := strings.Index(block, substring); idx >= 0 {
				return i, idx, len(substring)
			}
			continue
		}
		if idx, l := indexFold(block, substring); idx >= 0 {
			return i, idx, l
		}
	}
	return -1, -1, 0
}

// indexFold finds the first byte index in haystack where a substring of the
// same rune length as needle case-insensitively equals needle, using
// strings.EqualFold for comparison. Returns (-1, 0) if not found.
func indexFold(haystack, needle string) (int, int) {
	needleRunes := len([]rune(needle))
	runes := []rune(haystack)
	for i := range runes {
		if i+needleRunes > len(runes) {
			break
		}
		candidate := string(runes[i : i+needleRunes])
		if strings.EqualFold(candidate, needle) {
			// Translate the rune index back to a byte offset.
			byteOffset := len(string(runes[:i]))
			return byteOffset, len(candidate)
		}
	}
	return -1, 0
}

// writeFileAtomic writes content to path by writing to a temp file in the
// same directory and renaming it into place, so a crash mid-write cannot
// leave a truncated file behind.
func writeFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("builtin: creating directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("builtin: creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("builtin: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("builtin: closing %s: %w", path, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("builtin: setting permissions on %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("builtin: renaming into %s: %w", path, err)
	}
	return nil
}
