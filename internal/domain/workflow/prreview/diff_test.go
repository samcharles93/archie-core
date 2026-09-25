package prreview

import (
	"reflect"
	"testing"
)

// fileSummary is the part of a parsed FileChange these tests assert on: a
// file's identity and its geometry, without the hunk bodies other tests carry.
type fileSummary struct {
	Path         string
	PreviousPath string
	Status       string
	Language     string
	Added        int
	Removed      int
	Hunks        int
}

func summarize(files []FileChange) []fileSummary {
	summaries := make([]fileSummary, 0, len(files))
	for _, file := range files {
		summaries = append(summaries, fileSummary{
			Path:         file.Path,
			PreviousPath: file.PreviousPath,
			Status:       string(file.Status),
			Language:     file.Language,
			Added:        file.LinesAdded,
			Removed:      file.LinesRemoved,
			Hunks:        len(file.Hunks),
		})
	}
	return summaries
}

func TestParseDiffDescribesEachFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diff string
		want []fileSummary
	}{
		{
			name: "a modified file",
			diff: "diff --git a/internal/a.go b/internal/a.go\nindex 111..222 100644\n--- a/internal/a.go\n+++ b/internal/a.go\n@@ -1,2 +1,3 @@\n package a\n+var b = 1\n var c = 2\n",
			want: []fileSummary{{Path: "internal/a.go", Status: "modified", Language: "go", Added: 1, Hunks: 1}},
		},
		{
			name: "an added file",
			diff: "diff --git a/pkg/new.py b/pkg/new.py\nnew file mode 100644\n--- /dev/null\n+++ b/pkg/new.py\n@@ -0,0 +1,2 @@\n+import os\n+x = 1\n",
			want: []fileSummary{{Path: "pkg/new.py", Status: "added", Language: "python", Added: 2, Hunks: 1}},
		},
		{
			name: "a deleted file keeps the path it had",
			diff: "diff --git a/pkg/gone.py b/pkg/gone.py\ndeleted file mode 100644\n--- a/pkg/gone.py\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-import os\n-x = 1\n",
			want: []fileSummary{{Path: "pkg/gone.py", Status: "deleted", Language: "python", Removed: 2, Hunks: 1}},
		},
		{
			name: "a rename with changes",
			diff: "diff --git a/old.go b/new.go\nsimilarity index 90%\nrename from old.go\nrename to new.go\n--- a/old.go\n+++ b/new.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n",
			want: []fileSummary{{Path: "new.go", PreviousPath: "old.go", Status: "renamed", Language: "go", Added: 1, Hunks: 1}},
		},
		{
			name: "a rename with no content at all",
			diff: "diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n",
			want: []fileSummary{{Path: "new.go", PreviousPath: "old.go", Status: "renamed", Language: "go"}},
		},
		{
			name: "a mode-only change still names its file",
			diff: "diff --git a/tool.sh b/tool.sh\nold mode 100644\nnew mode 100755\n",
			want: []fileSummary{{Path: "tool.sh", Status: "modified", Language: "bash"}},
		},
		{
			name: "a multi-hunk file",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n@@ -40,1 +41,2 @@\n func f() {}\n+func g() {}\n",
			want: []fileSummary{{Path: "a.go", Status: "modified", Language: "go", Added: 2, Hunks: 2}},
		},
		{
			name: "two files in one diff",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\ndiff --git a/docs/x.md b/docs/x.md\n--- a/docs/x.md\n+++ b/docs/x.md\n@@ -1,1 +1,2 @@\n # x\n+text\n",
			want: []fileSummary{
				{Path: "a.go", Status: "modified", Language: "go", Added: 1, Hunks: 1},
				{Path: "docs/x.md", Status: "modified", Language: "markdown", Added: 1, Hunks: 1},
			},
		},
		{
			name: "a diff of nothing",
			diff: "",
			want: nil,
		},
		{
			name: "a plain unified diff opens its files from the --- header",
			diff: "--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n package a\n+var b = 1\n var c = 2\n--- a/b.go\n+++ b/b.go\n@@ -1,1 +1,2 @@\n package b\n+var x = 1\n",
			want: []fileSummary{
				{Path: "a.go", Status: "modified", Language: "go", Added: 1, Hunks: 1},
				{Path: "b.go", Status: "modified", Language: "go", Added: 1, Hunks: 1},
			},
		},
		{
			name: "a plain unified diff's added file",
			diff: "--- /dev/null\n+++ b/created.go\n@@ -0,0 +1,1 @@\n+package created\n",
			want: []fileSummary{{Path: "created.go", Status: "added", Language: "go", Added: 1, Hunks: 1}},
		},
		{
			name: "a plain unified diff's deleted file",
			diff: "--- a/gone.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package gone\n",
			want: []fileSummary{{Path: "gone.go", Status: "deleted", Language: "go", Removed: 1, Hunks: 1}},
		},
		{
			// A binary file's patch carries no hunks, so the ---/+++ headers
			// are absent and the mode line is the only statement of what the
			// change did. git's own output, byte for byte.
			name: "an added binary file is an addition",
			diff: "diff --git a/logo.png b/logo.png\nnew file mode 100644\nindex 0000000..8352675\nBinary files /dev/null and b/logo.png differ\n",
			want: []fileSummary{{Path: "logo.png", Status: "added"}},
		},
		{
			name: "a deleted binary file is a deletion",
			diff: "diff --git a/logo.png b/logo.png\ndeleted file mode 100644\nindex 8352675..0000000\nBinary files a/logo.png and /dev/null differ\n",
			want: []fileSummary{{Path: "logo.png", Status: "deleted"}},
		},
		{
			name: "an added empty file is an addition",
			diff: "diff --git a/empty.txt b/empty.txt\nnew file mode 100644\nindex 0000000..e69de29\n",
			want: []fileSummary{{Path: "empty.txt", Status: "added"}},
		},
		{
			name: "a deleted empty file is a deletion",
			diff: "diff --git a/empty.txt b/empty.txt\ndeleted file mode 100644\nindex e69de29..0000000\n",
			want: []fileSummary{{Path: "empty.txt", Status: "deleted"}},
		},
		{
			// A permission change is a modification; a change of object type
			// -- a symlink becoming a regular file, a submodule becoming a
			// file -- is a typechange, the status the change capture persists
			// as "typechange".
			name: "a file-type change is a typechange",
			diff: "diff --git a/link b/link\nold mode 120000\nnew mode 100644\nindex 4cbb553..7898192\n--- a/link\n+++ b/link\n@@ -1,1 +1,1 @@\n-target.txt\n+content\n",
			want: []fileSummary{{Path: "link", Status: "typechange", Added: 1, Removed: 1, Hunks: 1}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := summarize(ParseDiff(test.diff))
			if len(got) != len(test.want) {
				t.Fatalf("ParseDiff() described %+v, want %+v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("file %d = %+v, want %+v", i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestParseDiffDecodesGitQuotedPaths(t *testing.T) {
	t.Parallel()

	// git C-quotes a path it cannot write plainly: every byte outside ASCII
	// under the default core.quotePath, and a quote, a backslash or a tab
	// anywhere. FileChange.Path is the path a reviewer hands back to the
	// snapshot, so it has to be the path and not git's spelling of it.
	tests := []struct {
		name string
		diff string
		want []fileSummary
	}{
		{
			name: "a non-ASCII path is escaped as the bytes of its UTF-8",
			diff: "diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\n" +
				"index 572eb43..1e76f07 100644\n" +
				"--- \"a/caf\\303\\251.txt\"\n" +
				"+++ \"b/caf\\303\\251.txt\"\n" +
				"@@ -1,1 +1,2 @@\n" +
				" old\n" +
				"+b\n",
			// 303 251 is C3 A9: "é" in UTF-8.
			want: []fileSummary{{Path: "caf\u00e9.txt", Status: "modified", Added: 1, Hunks: 1}},
		},
		{
			name: "a quote is escaped in the path and decoded out of it",
			diff: "diff --git \"a/qu\\\"ote.txt\" \"b/qu\\\"ote.txt\"\n" +
				"index 7898192..422c2b7 100644\n" +
				"--- \"a/qu\\\"ote.txt\"\n" +
				"+++ \"b/qu\\\"ote.txt\"\n" +
				"@@ -1,1 +1,2 @@\n" +
				" old\n" +
				"+b\n",
			want: []fileSummary{{Path: `qu"ote.txt`, Status: "modified", Added: 1, Hunks: 1}},
		},
		{
			name: "a tab in a path is a tab once it is decoded",
			diff: "diff --git \"a/tab\\tname.txt\" \"b/tab\\tname.txt\"\n" +
				"--- \"a/tab\\tname.txt\"\n" +
				"+++ \"b/tab\\tname.txt\"\n",
			want: []fileSummary{{Path: "tab\tname.txt", Status: "modified"}},
		},
		{
			name: "a backslash is escaped twice and decoded once",
			diff: "diff --git \"a/back\\\\slash.txt\" \"b/back\\\\slash.txt\"\n" +
				"--- \"a/back\\\\slash.txt\"\n" +
				"+++ \"b/back\\\\slash.txt\"\n",
			want: []fileSummary{{Path: `back\slash.txt`, Status: "modified"}},
		},
		{
			name: "a quoted rename names both paths without their quoting",
			diff: "diff --git \"a/back\\\\slash.txt\" \"b/renamed\\\\back.txt\"\n" +
				"similarity index 100%\n" +
				"rename from \"back\\\\slash.txt\"\n" +
				"rename to \"renamed\\\\back.txt\"\n",
			want: []fileSummary{{Path: `renamed\back.txt`, PreviousPath: `back\slash.txt`, Status: "renamed"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := summarize(ParseDiff(test.diff))
			if len(got) != len(test.want) {
				t.Fatalf("ParseDiff() described %+v, want %+v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("file %d = %+v, want %+v", i, got[i], test.want[i])
				}
			}
		})
	}
}

// TestFileStatusIsTheCapturedChangeVocabulary pins this package's status words
// to internal/domain/workflow/task/changes.go, which persists a captured
// change's status as these strings. prreview imports the standard library only,
// so this test -- rather than a shared constant -- is what keeps the two
// vocabularies one. A status read off a diff and a status read off a capture
// have to be the same word, or a caller comparing them silently misses one.
func TestFileStatusIsTheCapturedChangeVocabulary(t *testing.T) {
	t.Parallel()

	captured := []struct {
		status FileStatus
		want   string
	}{
		{FileAdded, "added"},
		{FileModified, "modified"},
		{FileRemoved, "deleted"},
		{FileRenamed, "renamed"},
		{FileTypeChanged, "typechange"},
	}

	for _, test := range captured {
		if string(test.status) != test.want {
			t.Errorf("FileStatus %q, want %q as the change capture persists it", test.status, test.want)
		}
	}
}

func TestParseDiffHunkGeometry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diff string
		want []Hunk
	}{
		{
			name: "the header positions the hunk in both files",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +3,4 @@ func f() {\n package a\n+var b = 1\n",
			want: []Hunk{{OldStart: 1, OldCount: 2, NewStart: 3, NewCount: 4, Header: "@@ -1,2 +3,4 @@ func f() {", Lines: []string{" package a", "+var b = 1"}}},
		},
		{
			name: "an omitted count is one line",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -5 +6 @@\n package a\n",
			want: []Hunk{{OldStart: 5, OldCount: 1, NewStart: 6, NewCount: 1, Header: "@@ -5 +6 @@", Lines: []string{" package a"}}},
		},
		{
			name: "an added line that looks like a file header is body text",
			diff: "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1,1 +1,2 @@\n # a\n+++ title\n",
			want: []Hunk{{OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 2, Header: "@@ -1,1 +1,2 @@", Lines: []string{" # a", "+++ title"}}},
		},
		{
			name: "the no-newline marker is not a line",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-package a\n+package b\n\\ No newline at end of file\n",
			want: []Hunk{{OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1, Header: "@@ -1,1 +1,1 @@", Lines: []string{"-package a", "+package b"}}},
		},
		{
			name: "a header that cannot be positioned starts no hunk",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ not a hunk header @@\n+orphan\n",
			want: nil,
		},
		{
			// A removed line whose text begins "-- " and an added line
			// whose text begins "++ ": the shape of a header pair, and body
			// text of the hunk that is still waiting for lines.
			name: "a header pair inside an unfinished hunk is body text",
			diff: "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1,1 +1,2 @@\n--- old title\n+++ new title\n",
			want: []Hunk{{OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 2, Header: "@@ -1,1 +1,2 @@", Lines: []string{"--- old title", "+++ new title"}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			files := ParseDiff(test.diff)
			var got []Hunk
			if len(files) > 0 {
				got = files[0].Hunks
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("hunks = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestAddedLinesPositionsTheChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diff string
		want []AddedLine
	}{
		{
			name: "context lines advance the position without being additions",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n package a\n+var b = 1\n var c = 2\n+var d = 3\n",
			want: []AddedLine{{Path: "a.go", Line: 2, Text: "var b = 1"}, {Path: "a.go", Line: 4, Text: "var d = 3"}},
		},
		{
			name: "a removed line holds no position in the file the change produces",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,2 @@\n package a\n-var gone = 1\n+var kept = 2\n var tail = 3\n",
			want: []AddedLine{{Path: "a.go", Line: 2, Text: "var kept = 2"}},
		},
		{
			name: "a second hunk numbers from its own header",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n@@ -40,1 +41,2 @@\n func f() {}\n+func g() {}\n",
			want: []AddedLine{{Path: "a.go", Line: 2, Text: "var b = 1"}, {Path: "a.go", Line: 42, Text: "func g() {}"}},
		},
		{
			name: "an added line that looks like a file header is body text",
			diff: "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1,1 +1,2 @@\n # a\n+++ title\n",
			want: []AddedLine{{Path: "a.md", Line: 2, Text: "++ title"}},
		},
		{
			name: "an added file numbers from line one",
			diff: "diff --git a/new.go b/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1,2 @@\n+package new\n+var a = 1\n",
			want: []AddedLine{{Path: "new.go", Line: 1, Text: "package new"}, {Path: "new.go", Line: 2, Text: "var a = 1"}},
		},
		{
			name: "a deletion and a pure rename add nothing",
			diff: "diff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package gone\ndiff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n",
			want: nil,
		},
		{
			name: "the no-newline marker adds nothing",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n\\ No newline at end of file\n",
			want: []AddedLine{{Path: "a.go", Line: 2, Text: "var b = 1"}},
		},
		{
			name: "an empty diff adds nothing",
			diff: "",
			want: nil,
		},
		{
			name: "a plain unified diff numbers each file from its own hunks",
			diff: "--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n package a\n+var b = 1\n var c = 2\n--- a/b.go\n+++ b/b.go\n@@ -1,1 +1,2 @@\n package b\n+var x = 1\n",
			want: []AddedLine{{Path: "a.go", Line: 2, Text: "var b = 1"}, {Path: "b.go", Line: 2, Text: "var x = 1"}},
		},
		{
			name: "a plain unified diff's added file numbers from line one",
			diff: "--- /dev/null\n+++ b/created.go\n@@ -0,0 +1,2 @@\n+package created\n+var a = 1\n",
			want: []AddedLine{{Path: "created.go", Line: 1, Text: "package created"}, {Path: "created.go", Line: 2, Text: "var a = 1"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := AddedLines(ParseDiff(test.diff))
			if len(got) != len(test.want) {
				t.Fatalf("AddedLines() = %+v, want %+v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("AddedLines()[%d] = %+v, want %+v", i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestMapLinePlacesACommentOnTheDiff(t *testing.T) {
	t.Parallel()

	// The change produces four lines: 1 context, 2 added, 3 context, 4 added.
	const diff = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,4 @@\n package a\n-var gone = 1\n+var kept = 2\n var tail = 3\n+var extra = 4\n"

	tests := []struct {
		name string
		path string
		line int
		want LineKind
	}{
		{name: "an added line", path: "a.go", line: 2, want: LineAdded},
		{name: "a context line inside the hunk", path: "a.go", line: 3, want: LineContext},
		{name: "the last added line", path: "a.go", line: 4, want: LineAdded},
		{name: "the line after the hunk", path: "a.go", line: 5, want: LineOutside},
		{name: "a line the hunk never quoted", path: "a.go", line: 400, want: LineOutside},
		{name: "a file the diff does not touch", path: "b.go", line: 1, want: LineOutside},
	}

	files := ParseDiff(diff)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := MapLine(files, test.path, test.line); got != test.want {
				t.Errorf("MapLine(%q, %d) = %q, want %q", test.path, test.line, got, test.want)
			}
		})
	}

	t.Run("a deleted file has no line in the file the change produces", func(t *testing.T) {
		t.Parallel()

		deleted := ParseDiff("diff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package gone\n")
		if got := MapLine(deleted, "gone.go", 1); got != LineOutside {
			t.Errorf("MapLine(gone.go, 1) = %q, want %q", got, LineOutside)
		}
	})
}

func TestSummarizeFilesCountsTheChange(t *testing.T) {
	t.Parallel()

	const diff = "diff --git a/pkg/a.go b/pkg/a.go\n--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1,1 +1,3 @@\n package a\n+var b = 1\n+var c = 2\ndiff --git a/pkg/a_test.go b/pkg/a_test.go\n--- a/pkg/a_test.go\n+++ b/pkg/a_test.go\n@@ -1,1 +1,1 @@\n-package a\n+package a\n" +
		"diff --git a/docs/new.md b/docs/new.md\n--- /dev/null\n+++ b/docs/new.md\n@@ -0,0 +1,1 @@\n+docs\ndiff --git a/old.go b/old.go\n--- a/old.go\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-package old\ndiff --git a/from.go b/to.go\nrename from from.go\nrename to to.go\n"

	tests := []struct {
		name string
		diff string
		want DiffStats
	}{
		{
			name: "every status is counted and the test ratio is per code file",
			diff: diff,
			want: DiffStats{
				TotalFiles: 5, TotalAdditions: 4, TotalDeletions: 2,
				FilesAdded: 1, FilesModified: 2, FilesRemoved: 1, FilesRenamed: 1,
				TestFilesChanged: 1, TestToCodeRatio: 0.25,
			},
		},
		{
			name: "a change with no test files reports a zero ratio",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n",
			want: DiffStats{TotalFiles: 1, TotalAdditions: 1, FilesModified: 1},
		},
		{
			name: "a change of tests alone does not divide by zero",
			diff: "diff --git a/a_test.go b/a_test.go\n--- a/a_test.go\n+++ b/a_test.go\n@@ -1,1 +1,2 @@\n package a\n+var b = 1\n",
			want: DiffStats{TotalFiles: 1, TotalAdditions: 1, FilesModified: 1, TestFilesChanged: 1, TestToCodeRatio: 1},
		},
		{
			// The files a patch cannot show hunks for: binary, empty, and a
			// pure mode change. They add no lines, and they are still an
			// addition, a deletion and a typechange.
			name: "files with no hunks are counted by what they did",
			diff: "diff --git a/logo.png b/logo.png\nnew file mode 100644\nindex 0000000..8352675\nBinary files /dev/null and b/logo.png differ\n" +
				"diff --git a/empty.txt b/empty.txt\nnew file mode 100644\nindex 0000000..e69de29\n" +
				"diff --git a/gone.png b/gone.png\ndeleted file mode 100644\nindex 8352675..0000000\nBinary files a/gone.png and /dev/null differ\n" +
				"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\nindex e69de29..0000000\n" +
				"diff --git a/link b/link\nold mode 120000\nnew mode 100644\n",
			want: DiffStats{TotalFiles: 5, FilesAdded: 2, FilesRemoved: 2, FilesTypeChanged: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := SummarizeFiles(ParseDiff(test.diff)); got != test.want {
				t.Errorf("SummarizeFiles() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestClusterFilesGroupsByDirectory(t *testing.T) {
	t.Parallel()

	files := ParseDiff("diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1,1 +1,2 @@\n package main\n+var a = 1\n" +
		"diff --git a/docs/a.md b/docs/a.md\n--- a/docs/a.md\n+++ b/docs/a.md\n@@ -1,1 +1,2 @@\n # a\n+x\n" +
		"diff --git a/docs/b.md b/docs/b.md\n--- a/docs/b.md\n+++ b/docs/b.md\n@@ -1,1 +1,2 @@\n # b\n+x\n" +
		"diff --git a/internal/a.go b/internal/a.go\n--- a/internal/a.go\n+++ b/internal/a.go\n@@ -1,1 +1,2 @@\n package internal\n+var a = 1\n" +
		"diff --git a/internal/README.md b/internal/README.md\n--- a/internal/README.md\n+++ b/internal/README.md\n@@ -1,1 +1,2 @@\n # a\n+x\n")

	want := []Cluster{
		{ID: "cluster_0", Name: "docs", Files: []string{"docs/a.md", "docs/b.md"}, PrimaryLanguage: "markdown"},
		{ID: "cluster_1", Name: "internal", Files: []string{"internal/a.go", "internal/README.md"}, PrimaryLanguage: "go"},
		{ID: "cluster_2", Name: "root", Files: []string{"main.go"}, PrimaryLanguage: "go"},
	}

	if got := ClusterFiles(files); !reflect.DeepEqual(got, want) {
		t.Errorf("ClusterFiles() = %+v, want %+v", got, want)
	}
}

func TestClusterFilesBreaksLanguageTiesByFirstAppearance(t *testing.T) {
	t.Parallel()

	files := ParseDiff("diff --git a/pkg/a.md b/pkg/a.md\n--- a/pkg/a.md\n+++ b/pkg/a.md\n@@ -1,1 +1,2 @@\n # a\n+x\n" +
		"diff --git a/pkg/b.go b/pkg/b.go\n--- a/pkg/b.go\n+++ b/pkg/b.go\n@@ -1,1 +1,2 @@\n package pkg\n+var a = 1\n")

	got := ClusterFiles(files)
	if len(got) != 1 {
		t.Fatalf("ClusterFiles() = %+v, want one cluster", got)
	}
	// One Markdown file and one Go file: the first file of the directory
	// decides, so the same diff always names the same language.
	if got[0].PrimaryLanguage != "markdown" {
		t.Errorf("PrimaryLanguage = %q, want %q", got[0].PrimaryLanguage, "markdown")
	}
}
