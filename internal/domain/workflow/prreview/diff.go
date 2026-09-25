// Package prreview is the deterministic core of the PR review pipeline: diff
// parsing, change clustering, blast radius, evidence extraction, scoring,
// deduplication, line mapping and the review event. Everything the pipeline
// can compute is computed here, so identical findings always produce identical
// scores, order and verdicts, and the agents around them only reason.
//
// It imports the standard library only, and the workflow package imports it
// rather than the reverse: code that cannot reach the store, the forge or the
// worktree is code whose data boundary stays visible.
package prreview

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FileStatus is what a change did to one file. The values are the vocabulary
// the repository already persists a change's status in, so a status read off a
// diff and a status read off a capture are spelled the same word.
type FileStatus string

const (
	FileAdded    FileStatus = "added"
	FileModified FileStatus = "modified"
	FileRemoved  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
)

// LineKind says where a line sits relative to a diff.
type LineKind string

const (
	// LineAdded is a line the change introduces.
	LineAdded LineKind = "added"
	// LineContext is an unchanged line the hunk quotes. It is inside the diff,
	// so a review comment can be anchored to it.
	LineContext LineKind = "context"
	// LineOutside is a line no hunk of the diff contains. Most of a file is
	// this, and it is the difference between a comment a forge accepts and one
	// it rejects.
	LineOutside LineKind = "outside"
)

// Hunk is one unified-diff hunk: the header that positions it in both files
// and the body lines that follow it.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	// Header is the "@@ ... @@" line as the diff wrote it, section heading
	// included: it is what a reader needs when a comment's position is wrong.
	Header string
	// Lines are the body lines with their diff prefix intact (' ', '+' or
	// '-'). The marker git writes for a file with no trailing newline is not a
	// line and is not kept.
	Lines []string
}

// FileChange is one file of a diff: where it went, what happened to it, and
// the hunks that carry the change.
type FileChange struct {
	// Path is the file's path in the change's own tree -- the path a reviewer
	// opens. A deletion keeps the path the file had: a finding anchored to ""
	// is a finding nobody can act on.
	Path string
	// PreviousPath is the pre-change path of a rename, empty otherwise.
	PreviousPath string
	Status       FileStatus
	// Language is the file's language by extension, empty when the extension
	// is not one this package knows.
	Language     string
	LinesAdded   int
	LinesRemoved int
	Hunks        []Hunk
}

// AddedLine is one line a change adds, positioned in the file the change
// produces: the number a reviewer opens, not the diff's own offset.
type AddedLine struct {
	Path string
	Line int
	Text string
}

// DiffStats is the size and shape of a change, from which intake picks a
// review depth.
type DiffStats struct {
	TotalFiles       int
	TotalAdditions   int
	TotalDeletions   int
	FilesAdded       int
	FilesModified    int
	FilesRemoved     int
	FilesRenamed     int
	TestFilesChanged int
	// TestToCodeRatio is changed test files per changed non-test file. A
	// change that touches no non-test file reports its own test count rather
	// than dividing by zero.
	TestToCodeRatio float64
}

// Cluster is a group of changed files that belong together.
type Cluster struct {
	ID              string
	Name            string
	Files           []string
	PrimaryLanguage string
}

// rootClusterName is the cluster of the files at the repository root, which
// have no directory to be named after.
const rootClusterName = "root"

const (
	gitFilePrefix = "diff --git "
	devNull       = "/dev/null"
)

// gitHeaderPaths reads both paths out of a "diff --git" line. git C-quotes a
// path that needs escaping, and a quoted line is left to the ---/+++ headers:
// those name the file in every section that has content, and this is only the
// fallback for the sections that have none (a pure rename, a mode change).
func gitHeaderPaths(line string) (oldPath, newPath string) {
	rest := strings.TrimPrefix(line, gitFilePrefix)
	if strings.HasPrefix(rest, `"`) {
		return "", ""
	}
	split := strings.LastIndex(rest, " b/")
	if split < 0 {
		return "", ""
	}
	return strings.TrimPrefix(rest[:split], "a/"), rest[split+len(" b/"):]
}

// headerPath reads the path out of a "--- "/"+++ " header: "b/main.go" in
// git's own output, quoted when the path carries something git has to escape,
// and tab-separated from a timestamp in a plain unified diff.
func headerPath(field string) string {
	filePath := strings.TrimSpace(field)
	if tab := strings.IndexByte(filePath, '\t'); tab >= 0 {
		filePath = filePath[:tab]
	}
	filePath = strings.Trim(filePath, `"`)
	return strings.TrimPrefix(strings.TrimPrefix(filePath, "b/"), "a/")
}

// hunkHeaderRE reads the position a hunk header gives it in both files. Both
// counts are optional in the format, and the section heading after the second
// "@@" may contain anything, so nothing after it is matched.
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// fileInProgress is the parser's state for the file section it is inside.
type fileInProgress struct {
	change  FileChange
	oldPath string
	// hunkAt is the index of the hunk body lines are read into, or -1 outside
	// a hunk. It is an index rather than a pointer because appending to the
	// hunks slice moves every element.
	hunkAt int
}

// newFileInProgress starts the file section a "diff --git" line opens. The
// header's own paths seed it, so a section with no content still has a path.
func newFileInProgress(line string) *fileInProgress {
	oldPath, newPath := gitHeaderPaths(line)
	return &fileInProgress{
		change:  FileChange{Path: newPath, Status: FileModified},
		oldPath: oldPath,
		hunkAt:  -1,
	}
}

// note reads one of the file's own header lines: the old and new paths, the
// /dev/null side that makes a file an addition or a deletion, and the rename
// pair. Anything else the section says -- index, mode, similarity -- says
// nothing about where the change is, which is all findings are positioned by.
func (f *fileInProgress) note(line string) {
	switch {
	case strings.HasPrefix(line, "--- "):
		f.oldPath = headerPath(strings.TrimPrefix(line, "--- "))
		if f.oldPath == devNull {
			f.change.Status = FileAdded
			f.oldPath = ""
		}
	case strings.HasPrefix(line, "+++ "):
		target := headerPath(strings.TrimPrefix(line, "+++ "))
		if target == devNull {
			f.change.Status = FileRemoved
			return
		}
		f.change.Path = target
	case strings.HasPrefix(line, "rename from "):
		f.oldPath = strings.TrimPrefix(line, "rename from ")
		f.change.Status = FileRenamed
	case strings.HasPrefix(line, "rename to "):
		f.change.Path = strings.TrimPrefix(line, "rename to ")
		f.change.Status = FileRenamed
	}
}

// startHunk opens the hunk a header line describes. A header this package
// cannot position lines by starts no hunk, so the body after it is read as
// nothing rather than anchored to a guessed position.
func (f *fileInProgress) startHunk(line string) {
	match := hunkHeaderRE.FindStringSubmatch(line)
	if match == nil {
		f.hunkAt = -1
		return
	}
	f.change.Hunks = append(f.change.Hunks, Hunk{
		OldStart: headerNumber(match[1]),
		OldCount: headerCount(match[2]),
		NewStart: headerNumber(match[3]),
		NewCount: headerCount(match[4]),
		Header:   line,
	})
	f.hunkAt = len(f.change.Hunks) - 1
}

// body folds one line of hunk body into the change: its count and the raw
// line. Outside a hunk the same bytes are a file header, so the caller decides
// which of the two it is; this is only ever called inside one.
func (f *fileInProgress) body(line string) {
	switch {
	case line == "":
		// The empty element a trailing newline leaves behind: every real line
		// of a hunk carries its prefix, so an unprefixed empty line is the end
		// of the text, not a line.
	case strings.HasPrefix(line, `\`):
		// git's "\ No newline at end of file" marker describes the line before
		// it and holds no position of its own.
	case strings.HasPrefix(line, "+"):
		f.change.LinesAdded++
		f.hunk().Lines = append(f.hunk().Lines, line)
	case strings.HasPrefix(line, "-"):
		f.change.LinesRemoved++
		f.hunk().Lines = append(f.hunk().Lines, line)
	default:
		// A context line advances the position without being the change.
		f.hunk().Lines = append(f.hunk().Lines, line)
	}
}

// hunk is the hunk body() is reading into. Every caller has checked hunkAt.
func (f *fileInProgress) hunk() *Hunk { return &f.change.Hunks[f.hunkAt] }

// close finishes the section. A section that never named a new path keeps the
// old one, so a deletion is still anchored to the file it deleted; the old
// path survives as PreviousPath only when the change moved the file.
func (f *fileInProgress) close() FileChange {
	change := f.change
	if change.Path == "" {
		change.Path = f.oldPath
	}
	if f.oldPath != "" && f.oldPath != change.Path {
		change.PreviousPath = f.oldPath
	}
	change.Language = language(change.Path)
	return change
}

// ParseDiff parses git's unified diff into per-file changes: the file sections
// "diff --git" opens, their rename and /dev/null headers, and the hunks that
// carry the change. It is the one parser: every position, count and path this
// package reports is read here.
func ParseDiff(diff string) []FileChange {
	var (
		files []FileChange
		file  *fileInProgress
	)
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, gitFilePrefix):
			if file != nil {
				files = append(files, file.close())
			}
			file = newFileInProgress(line)
		case file == nil:
			// Text before the first "diff --git" belongs to no file, so there
			// is nothing it could position a finding against.
		case strings.HasPrefix(line, "@@"):
			file.startHunk(line)
		case file.hunkAt < 0:
			file.note(line)
		default:
			file.body(line)
		}
	}
	if file != nil {
		files = append(files, file.close())
	}
	return files
}

// headerNumber reads a start line out of a hunk header. The regular expression
// that matched it guarantees digits.
func headerNumber(digits string) int {
	number, _ := strconv.Atoi(digits)
	return number
}

// headerCount reads a line count out of a hunk header, defaulting to the one
// line the format implies when the count is omitted.
func headerCount(digits string) int {
	if digits == "" {
		return 1
	}
	return headerNumber(digits)
}

// position is one line of a hunk placed in the file the change produces.
type position struct {
	Line int
	Kind LineKind
	Text string
}

// positions walks a hunk's body, numbering the lines that exist in the file
// the change produces. A removed line is not in that file, so it holds no
// position here: its coordinates belong to the old file.
func (h Hunk) positions() []position {
	positions := make([]position, 0, len(h.Lines))
	line := h.NewStart
	for _, raw := range h.Lines {
		switch {
		case strings.HasPrefix(raw, "-"):
			// Removed: no position in the file the change produces.
		case strings.HasPrefix(raw, "+"):
			positions = append(positions, position{Line: line, Kind: LineAdded, Text: strings.TrimPrefix(raw, "+")})
			line++
		default:
			positions = append(positions, position{Line: line, Kind: LineContext, Text: strings.TrimPrefix(raw, " ")})
			line++
		}
	}
	return positions
}

// AddedLines returns every line the change adds, in file and hunk order. Only
// added lines: a removed line has no position in the file the change produces,
// and a context line is not something the change did.
func AddedLines(files []FileChange) []AddedLine {
	var added []AddedLine
	for _, file := range files {
		for _, hunk := range file.Hunks {
			for _, placed := range hunk.positions() {
				if placed.Kind != LineAdded {
					continue
				}
				added = append(added, AddedLine{Path: file.Path, Line: placed.Line, Text: placed.Text})
			}
		}
	}
	return added
}

// MapLine maps a line of the file a change produces onto the diff: a line the
// change added, a line a hunk quotes as context, or a line no hunk contains.
func MapLine(files []FileChange, path string, line int) LineKind {
	for _, file := range files {
		if file.Path != path {
			continue
		}
		for _, hunk := range file.Hunks {
			for _, placed := range hunk.positions() {
				if placed.Line == line {
					return placed.Kind
				}
			}
		}
	}
	return LineOutside
}

// SummarizeFiles computes the aggregate statistics of a parsed change.
func SummarizeFiles(files []FileChange) DiffStats {
	stats := DiffStats{TotalFiles: len(files)}
	for _, file := range files {
		stats.TotalAdditions += file.LinesAdded
		stats.TotalDeletions += file.LinesRemoved
		switch file.Status {
		case FileAdded:
			stats.FilesAdded++
		case FileRemoved:
			stats.FilesRemoved++
		case FileRenamed:
			stats.FilesRenamed++
		default:
			stats.FilesModified++
		}
		if isTestFile(file.Path) {
			stats.TestFilesChanged++
		}
	}

	codeFiles := max(stats.TotalFiles-stats.TestFilesChanged, 1)
	stats.TestToCodeRatio = float64(stats.TestFilesChanged) / float64(codeFiles)
	return stats
}

// ClusterFiles groups changed files by the directory they live in, the
// cheapest grouping that puts a Go package, a Python module directory or a
// feature folder in one cluster. Files at the repository root form one cluster.
func ClusterFiles(files []FileChange) []Cluster {
	byDirectory := map[string][]FileChange{}
	directories := make([]string, 0, len(files))
	for _, file := range files {
		directory := ""
		if slash := strings.LastIndexByte(file.Path, '/'); slash >= 0 {
			directory = file.Path[:slash]
		}
		if directory == "" {
			directory = rootClusterName
		}
		if _, seen := byDirectory[directory]; !seen {
			directories = append(directories, directory)
		}
		byDirectory[directory] = append(byDirectory[directory], file)
	}
	sort.Strings(directories)

	clusters := make([]Cluster, 0, len(directories))
	for index, directory := range directories {
		clusters = append(clusters, newCluster(index, directory, byDirectory[directory]))
	}
	return clusters
}

// newCluster names one directory's group and gives it the language most of its
// files are written in. Ties go to the earlier file, so the same change always
// reports the same primary language.
func newCluster(index int, directory string, files []FileChange) Cluster {
	cluster := Cluster{ID: fmt.Sprintf("cluster_%d", index), Name: directory}
	counts := map[string]int{}
	for _, file := range files {
		cluster.Files = append(cluster.Files, file.Path)
		if file.Language == "" {
			continue
		}
		counts[file.Language]++
		if counts[file.Language] > counts[cluster.PrimaryLanguage] {
			cluster.PrimaryLanguage = file.Language
		}
	}
	return cluster
}

// testPathMarkers are the path shapes read as a test file. It is a path test,
// not a content test: a file under tests/ is the change's own evidence that
// the change is a test change, whatever it contains.
var testPathMarkers = []string{"test_", "_test.", ".test.", "tests/", "test/", "__tests__/", "spec/"}

// isTestFile reports whether a path names a test file.
func isTestFile(filePath string) bool {
	lowered := strings.ToLower(filePath)
	for _, marker := range testPathMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// language names the language of a changed file by its extension, empty when
// the extension is not one this package knows. The name is the word a reader
// sees in the change's anatomy, not the extension.
func language(filePath string) string {
	return languages[strings.ToLower(filepath.Ext(filePath))]
}

// languages are the extensions this package names a language for.
var languages = map[string]string{
	".py":    "python",
	".js":    "javascript",
	".jsx":   "javascript",
	".ts":    "typescript",
	".tsx":   "typescript",
	".go":    "go",
	".rs":    "rust",
	".java":  "java",
	".rb":    "ruby",
	".cpp":   "cpp",
	".c":     "c",
	".cs":    "csharp",
	".swift": "swift",
	".kt":    "kotlin",
	".scala": "scala",
	".php":   "php",
	".sh":    "bash",
	".yaml":  "yaml",
	".yml":   "yaml",
	".json":  "json",
	".md":    "markdown",
	".sql":   "sql",
	".html":  "html",
	".css":   "css",
}
