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
	// FileTypeChanged is a file whose object type changed: a symlink that
	// became a regular file, a submodule that became one. A permission change
	// on the same type is a modification, not a typechange.
	FileTypeChanged FileStatus = "typechange"
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
	FilesTypeChanged int
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
	// oldSidePrefix and newSidePrefix are the a/ and b/ git puts in front of
	// the two sides of its own diff. They are not part of the path: a finding
	// anchored to "b/x" names a file the snapshot does not have. A plain
	// unified diff written by another tool carries no prefix, and a path whose
	// first directory really is "a" or "b" keeps it, because only its own
	// side's prefix is stripped.
	oldSidePrefix = "a/"
	newSidePrefix = "b/"
)

// gitHeaderPaths reads both paths out of a "diff --git" line, each without its
// side prefix. git writes the paths plainly, or C-quoted when one of them holds
// something it has to escape -- and a quoted line names both sides in quotes,
// because a path that needs escaping is exactly a path this line could not be
// split on a space.
func gitHeaderPaths(line string) (oldPath, newPath string) {
	rest := strings.TrimPrefix(line, gitFilePrefix)
	if oldField, remaining, quoted := quotedField(rest); quoted {
		newField, _, _ := quotedField(remaining)
		return strings.TrimPrefix(unquotePath(oldField), oldSidePrefix),
			strings.TrimPrefix(unquotePath(newField), newSidePrefix)
	}
	split := strings.LastIndex(rest, " b/")
	if split < 0 {
		return "", ""
	}
	return strings.TrimPrefix(rest[:split], oldSidePrefix), strings.TrimRight(rest[split+len(" b/"):], " \r")
}

// headerPath reads the path out of a "--- " or "+++ " header: git's "b/main.go",
// a plain unified diff's path with a timestamp after a tab, a /dev/null side, or
// the C-quoted form git writes for a path it has to escape. side is the prefix
// that side of a git diff carries; it is stripped here and only here, so the
// other side's prefix survives in a path that happens to start with it.
func headerPath(field, side string) string {
	if quoted, _, ok := quotedField(field); ok {
		return strings.TrimPrefix(unquotePath(quoted), side)
	}
	if tab := strings.IndexByte(field, '\t'); tab >= 0 {
		field = field[:tab]
	}
	return strings.TrimPrefix(unquotePath(strings.TrimRight(field, " \r")), side)
}

// quotedField reads the C-quoted field at the front of a header line -- the
// quotes included -- and whatever follows it: the second quoted path of a
// "diff --git" line, a timestamp, or nothing at all.
func quotedField(rest string) (field, remaining string, ok bool) {
	if !strings.HasPrefix(rest, `"`) {
		return "", "", false
	}
	for index := 1; index < len(rest); index++ {
		switch rest[index] {
		case '\\':
			// The escaped byte, whatever it is. An octal run needs no special
			// handling here: its digits can hold neither a backslash nor a
			// quote.
			index++
		case '"':
			return rest[:index+1], strings.TrimLeft(rest[index+1:], " "), true
		}
	}
	return "", "", false
}

// unquoteEscapes maps the named C escapes git writes onto the bytes they stand
// for.
var unquoteEscapes = map[byte]byte{
	'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v',
	'\\': '\\', '"': '"',
}

// unquotePath decodes the C-style quoting git writes around a path. The escapes
// are C's: the named ones for the bytes that cannot be written, and up to three
// octal digits for every other byte, which is how a non-ASCII path comes out of
// the default core.quotePath. A field that is not quoted is already the path.
func unquotePath(field string) string {
	if !strings.HasPrefix(field, `"`) || len(field) < 2 {
		return field
	}
	// The field ends with the quote git closed it with, and an escaped quote
	// before that is written \", so the last byte is never the path's.
	last := len(field) - 1
	var path strings.Builder
	path.Grow(last)
	for index := 1; index < last; index++ {
		char := field[index]
		if char != '\\' {
			path.WriteByte(char)
			continue
		}
		index++
		if index >= last {
			// A trailing backslash escapes nothing, and the byte that follows
			// it is the quote this path was closed with.
			break
		}
		if named, known := unquoteEscapes[field[index]]; known {
			path.WriteByte(named)
			continue
		}
		if field[index] < '0' || field[index] > '7' {
			// An escape this decoder does not know: the byte after the
			// backslash is the byte git wrote.
			path.WriteByte(field[index])
			continue
		}
		octal := 0
		for digits := 0; digits < 3 && index < last && field[index] >= '0' && field[index] <= '7'; digits++ {
			octal = octal<<3 | int(field[index]-'0')
			index++
		}
		// The loop above leaves index on the first byte past the digits, and
		// the for statement moves it one further.
		index--
		path.WriteByte(byte(octal))
	}
	return path.String()
}

// fileType is the object type a git mode names. A change between two of them --
// a regular file becoming a symlink, a submodule becoming a file -- is a
// file-type change, which git reports as "typechange"; a change between two
// permission sets of the same type is a modification.
func fileType(mode int) int { return mode & 0o170000 }

// parseFileType reads the object type out of the mode an "old mode" or
// "new mode" line carries, and is zero when the line holds no mode this
// package can read.
func parseFileType(field string) int {
	mode, err := strconv.ParseInt(strings.TrimSpace(field), 8, 32)
	if err != nil || mode <= 0 {
		return 0
	}
	return fileType(int(mode))
}

// hunkHeaderRE reads the position a hunk header gives it in both files. Both
// counts are optional in the format, and the section heading after the second
// "@@" may contain anything, so nothing after it is matched.
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// fileInProgress is the parser's state for the file section it is inside. The
// section's headers are read into it as facts -- which paths, what kind of
// change, which object type -- and close() turns those facts into one status,
// so the status is a statement about the section and not about the line of it
// that happened to come last.
type fileInProgress struct {
	change  FileChange
	oldPath string
	// hunkAt is the index of the hunk body lines are read into, or -1 outside
	// a hunk. It is an index rather than a pointer because appending to the
	// hunks slice moves every element.
	hunkAt int
	// oldSeen and newSeen are the old-file and new-file lines the open hunk
	// has already been given. A hunk is over when both have reached the counts
	// its header declared, which is how the format ends a hunk with no line
	// that says so, and what lets the next file's "--- " header be read as one.
	oldSeen int
	newSeen int
	// headerSeen records that the section has read its "+++ " line, so a later
	// "--- " header opens the next file rather than restating this one's old
	// path.
	headerSeen bool
	// added, removed, renamed and the two object types are what the section's
	// own metadata lines said the change did. A section whose patch has no
	// hunks -- a binary file, an empty file, a pure rename -- states it here
	// and nowhere else.
	added   bool
	removed bool
	renamed bool
	oldType int
	newType int
}

// newFileInProgress starts the file section a "diff --git" line opens. The
// header's own paths seed it, so a section with no content still has a path.
func newFileInProgress(line string) *fileInProgress {
	oldPath, newPath := gitHeaderPaths(line)
	return &fileInProgress{change: FileChange{Path: newPath}, oldPath: oldPath, hunkAt: -1}
}

// newSectionInProgress starts the file section a "--- "/"+++ " header pair
// opens, read from its old-path line: the shape of a plain unified diff, which
// has no "diff --git" line and says where a file begins with that pair alone.
func newSectionInProgress(line string) *fileInProgress {
	file := &fileInProgress{hunkAt: -1}
	file.note(line)
	return file
}

// note reads one of the file's own header lines: the old and new paths, the
// /dev/null side that makes a file an addition or a deletion, the rename pair,
// and the mode lines -- which say the same thing for a file whose patch has no
// hunks at all. Anything else the section says -- index, similarity -- says
// nothing about where the change is, which is all findings are positioned by.
func (f *fileInProgress) note(line string) {
	switch {
	case strings.HasPrefix(line, "--- "):
		f.oldPath = headerPath(strings.TrimPrefix(line, "--- "), oldSidePrefix)
		if f.oldPath == devNull {
			f.added = true
			f.oldPath = ""
		}
	case strings.HasPrefix(line, "+++ "):
		f.headerSeen = true
		target := headerPath(strings.TrimPrefix(line, "+++ "), newSidePrefix)
		if target == devNull {
			f.removed = true
			return
		}
		f.change.Path = target
	case strings.HasPrefix(line, "rename from "):
		f.oldPath = unquotePath(strings.TrimPrefix(line, "rename from "))
		f.renamed = true
	case strings.HasPrefix(line, "rename to "):
		f.change.Path = unquotePath(strings.TrimPrefix(line, "rename to "))
		f.renamed = true
	case strings.HasPrefix(line, "new file mode "):
		f.added = true
	case strings.HasPrefix(line, "deleted file mode "):
		f.removed = true
	case strings.HasPrefix(line, "old mode "):
		f.oldType = parseFileType(strings.TrimPrefix(line, "old mode "))
	case strings.HasPrefix(line, "new mode "):
		f.newType = parseFileType(strings.TrimPrefix(line, "new mode "))
	}
}

// status is what the section's own headers said the change did to the file,
// read off every fact the section stated at once. A rename wins over a mode
// change, because git reports a file that was renamed and had its mode changed
// as a rename; an addition or a deletion wins over a type change, because a
// file that was not there has no old type; and a mode change between two
// permission sets of one object type is a modification, not a typechange.
func (f *fileInProgress) status() FileStatus {
	switch {
	case f.renamed:
		return FileRenamed
	case f.added:
		return FileAdded
	case f.removed:
		return FileRemoved
	case f.oldType != 0 && f.newType != 0 && f.oldType != f.newType:
		return FileTypeChanged
	default:
		return FileModified
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
	f.oldSeen = 0
	f.newSeen = 0
}

// hunkOpen reports whether the next line is a line of the hunk being read: a
// hunk is open and the counts its header declared still have lines to place.
func (f *fileInProgress) hunkOpen() bool {
	if f.hunkAt < 0 {
		return false
	}
	hunk := f.change.Hunks[f.hunkAt]
	return f.oldSeen < hunk.OldCount || f.newSeen < hunk.NewCount
}

// headerPair reports whether the line at index opens a file section: the "--- "
// old-path header with the "+++ " new-path header on the next line. That pair
// is how a plain unified diff -- one written by a tool that is not git -- says
// where a file begins.
func headerPair(lines []string, index int) bool {
	if !strings.HasPrefix(lines[index], "--- ") {
		return false
	}
	return index+1 < len(lines) && strings.HasPrefix(lines[index+1], "+++ ")
}

// opensNextFile reports whether the header pair at index begins the next file
// rather than being body text of this one. A pair is body text -- a removed
// line whose text begins "-- " followed by an added line whose text begins
// "++ " -- while the open hunk is still waiting for the lines its header
// counted; and a section has read its own "+++ " line before a pair can be
// another file's, because the pair of the section being opened is its own
// header.
func (f *fileInProgress) opensNextFile(lines []string, index int) bool {
	return f.headerSeen && !f.hunkOpen() && headerPair(lines, index)
}

// body folds one line of hunk body into the change: its count, its side, and
// the raw line. Outside a hunk the same bytes are a file header, so the caller
// decides which of the two it is; this is only ever called inside one.
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
		f.newSeen++
		f.hunk().Lines = append(f.hunk().Lines, line)
	case strings.HasPrefix(line, "-"):
		f.change.LinesRemoved++
		f.oldSeen++
		f.hunk().Lines = append(f.hunk().Lines, line)
	default:
		// A context line advances the position in both files without being
		// the change.
		f.oldSeen++
		f.newSeen++
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
	change.Status = f.status()
	if change.Path == "" {
		change.Path = f.oldPath
	}
	if f.oldPath != "" && f.oldPath != change.Path {
		change.PreviousPath = f.oldPath
	}
	change.Language = language(change.Path)
	return change
}

// ParseDiff parses a unified diff into per-file changes: the file sections
// "diff --git" opens, the sections a "--- "/"+++ " header pair opens in a diff
// that has no such line, their rename, mode and /dev/null headers, and the
// hunks that carry the change. It is the one parser: every position, count and
// path this package reports is read here.
func ParseDiff(diff string) []FileChange {
	var (
		files []FileChange
		file  *fileInProgress
	)
	// The diff is read as a list rather than as a stream: whether a "--- "
	// header opens the next file or is a removed line is decided by the header
	// that follows it.
	lines := strings.Split(diff, "\n")
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, gitFilePrefix):
			if file != nil {
				files = append(files, file.close())
			}
			file = newFileInProgress(line)
		case file == nil:
			// Text before the first file section belongs to no file, so there
			// is nothing it could position a finding against -- except the
			// header pair a plain unified diff begins with.
			if headerPair(lines, index) {
				file = newSectionInProgress(line)
			}
		case strings.HasPrefix(line, "@@"):
			// A hunk header is read as one even while a hunk is open: no body
			// line of a diff begins with "@@", so this is the next hunk of the
			// file, or a recovery from a section whose counts were wrong.
			file.startHunk(line)
		case file.opensNextFile(lines, index):
			// A plain unified diff has no line that ends a file, so the next
			// one begins where the previous one's last hunk ended.
			files = append(files, file.close())
			file = newSectionInProgress(line)
		case file.hunkAt >= 0:
			// Every line of a hunk body carries a ' ', '+' or '-' prefix, so a
			// line that looks like a file header is body text while a hunk is
			// being read.
			file.body(line)
		default:
			file.note(line)
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
		case FileTypeChanged:
			stats.FilesTypeChanged++
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
