// Lifted from tau internal/agent/tools/read.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ReadParams are the parameters for the read tool.
type ReadParams struct {
	Path   string `json:"path"`
	File   string `json:"file,omitempty"`   // compatibility alias used by some providers
	Offset int    `json:"offset,omitempty"` // start line (1-based)
	Limit  int    `json:"limit,omitempty"`  // max lines to read
	Full   bool   `json:"full,omitempty"`   // explicitly allow a full-file response
}

// DefaultReadLines is the bounded line count used when no explicit read limit
// or full-file request is supplied.
const DefaultReadLines = 400

var readSchema = Schema{
	Name:        "read",
	Description: fmt.Sprintf("Read file contents; pointed at a directory it returns a listing. Omitted limits return at most %d lines; set full:true only when the complete file is genuinely needed. Output is always capped at %d lines or %s and includes a continuation offset.", DefaultReadLines, DefaultMaxLines, FormatSize(DefaultMaxBytes)),
	// NOTE: file is a compatibility alias for path; the executor
	// handles the fallback (file → path) and returns a clear error
	// when both are empty. We intentionally do NOT use anyOf here
	// because the OpenAI Responses API rejects schemas with
	// anyOf/oneOf/allOf at the top level of the parameters object.
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute or relative file path to read"
			},
			"file": {
				"type": "string",
				"description": "Compatibility alias for path. Prefer path."
			},
			"offset": {
				"type": "integer",
				"description": "Start reading from this line number (1-based). Defaults to 1."
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of lines to read. Defaults to 400 unless full is true."
			},
			"full": {
				"type": "boolean",
				"description": "Return the whole file up to the global safety cap, and resend lines you have already been shown. Defaults to false. Lines already read from an unchanged file are skipped unless this is set."
			}
		}
	}`),
}

const (
	// maxPathSuggestions bounds how many alternatives a not-found read offers.
	maxPathSuggestions = 5

	// maxSuggestionScan bounds the walk behind those suggestions. A miss is
	// already an error path, so it must stay cheap on a large workspace.
	maxSuggestionScan = 20000
)

// suggestPaths looks for files sharing the missing path's basename. Reads that
// fail on a plausible-but-wrong path cost a round trip plus a follow-up find;
// naming the real location lets the model correct itself immediately.
//
// The match is on basename alone, which is loose, but a wrong suggestion costs
// one line of output while no suggestion costs a whole round trip.
func suggestPaths(cwd, missing string) string {
	if cwd == "" {
		return ""
	}
	want := filepath.Base(missing)
	if want == "" || want == "." || want == string(filepath.Separator) {
		return ""
	}

	// If the directory exists, its contents are the most useful answer: the
	// model had the location right and only the filename wrong. This is the
	// shape of the real misses (internal/theme/theme.go, where the directory
	// exists but holds colors.go), and it avoids the walk entirely.
	if dir := filepath.Dir(missing); dir != "" {
		if entries, err := os.ReadDir(dir); err == nil {
			names := make([]string, 0, maxPathSuggestions)
			for _, e := range entries {
				if len(names) >= maxPathSuggestions {
					names = append(names, "...")
					break
				}
				names = append(names, e.Name())
			}
			if len(names) > 0 {
				rel, relErr := filepath.Rel(cwd, dir)
				if relErr != nil {
					rel = dir
				}
				return fmt.Sprintf("%s exists and contains: %s", rel, strings.Join(names, ", "))
			}
		}
	}

	var found []string
	scanned := 0

	_ = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree just yields no suggestions
		}
		if scanned++; scanned > maxSuggestionScan {
			return fs.SkipAll
		}
		if d.IsDir() {
			if skipSuggestionDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != want {
			return nil
		}
		if rel, relErr := filepath.Rel(cwd, path); relErr == nil {
			found = append(found, rel)
		}
		if len(found) >= maxPathSuggestions {
			return fs.SkipAll
		}
		return nil
	})

	if len(found) == 0 {
		return ""
	}
	return "did you mean: " + strings.Join(found, ", ")
}

// skipSuggestionDir keeps the suggestion walk out of directories that are large
// and never the answer.
func skipSuggestionDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".cache":
		return true
	}
	return false
}

// maxDirEntries bounds the listing returned when read is pointed at a
// directory. Large directories are truncated rather than refused.
const maxDirEntries = 200

// listDirResult renders a bounded directory listing. display is the path as
// the caller wrote it, so the notice echoes their own spelling.
func listDirResult(path, display string) (Result, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return Result{Content: fmt.Sprintf("error reading directory: %v", err), IsError: true}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s is a directory, not a file. Listing:\n", display)
	for i, e := range entries {
		if i >= maxDirEntries {
			fmt.Fprintf(&b, "\n[%d more entries; use find for a filtered search]", len(entries)-maxDirEntries)
			break
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
			continue
		}
		fmt.Fprintf(&b, "%s\n", e.Name())
	}

	return Result{Content: strings.TrimRight(b.String(), "\n"), Truncated: len(entries) > maxDirEntries}, nil
}

// NewReadTool creates the built-in read tool.
func NewReadTool(cwd string, rt *ReadTracker) Tool {
	return Tool{
		Schema:  readSchema,
		Source:  "builtin",
		Execute: makeReadExecutor(cwd, rt),
	}
}

func makeReadExecutor(cwd string, rt *ReadTracker) Executor {
	return func(ctx context.Context, params json.RawMessage, _ UIBridge) (Result, error) {
		var p ReadParams
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{Content: fmt.Sprintf("invalid parameters: %v", err), IsError: true}, nil
		}

		if p.Offset < 0 {
			return Result{Content: "offset must be >= 0", IsError: true}, nil
		}
		if p.Limit < 0 {
			return Result{Content: "limit must be >= 0", IsError: true}, nil
		}
		if strings.TrimSpace(p.Path) == "" {
			p.Path = p.File
		}
		if strings.TrimSpace(p.Path) == "" {
			return Result{Content: "path is required (file is accepted as an alias)", IsError: true, ErrorKind: "invalid_arguments"}, nil
		}
		// Record whether the caller asked for a specific slice before the
		// default is applied. An explicit limit that happens to equal the
		// default is still an explicit content request, so it must not be
		// mistaken for "unspecified" by the outline check below.
		rangeRequested := p.Offset > 0 || p.Limit > 0

		if p.Limit == 0 && !p.Full {
			p.Limit = DefaultReadLines
		}

		_, cancel := context.WithTimeout(ctx, DefaultToolTimeout)
		defer cancel()

		path := resolvePath(cwd, p.Path)

		if !isReadConfined(cwd, path) {
			return Result{Content: "path escapes working directory", IsError: true, ErrorKind: "sandbox_escape"}, nil
		}

		info, err := os.Stat(path)
		if err != nil {
			msg := fmt.Sprintf("error stating file: %v", err)
			if hint := suggestPaths(cwd, path); hint != "" {
				msg += "\n" + hint
			}
			return Result{Content: msg, IsError: true, ErrorKind: "not_found"}, nil
		}
		if info.IsDir() {
			// The intent is unambiguous, so answer it rather than spending a
			// round trip on an error. Deliberately returned before MarkRead:
			// a directory must never satisfy the read-before-write check.
			return listDirResult(path, p.Path)
		}
		if info.Size() > maxReadBytes {
			return Result{Content: fmt.Sprintf("file too large (%s > %s)", FormatSize(int(info.Size())), FormatSize(maxReadBytes)), IsError: true}, nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return Result{Content: fmt.Sprintf("error reading file: %v", err), IsError: true}, nil
		}

		// Record the read so mutation tools can enforce read-before-write.
		// Only record after a successful ReadFile so we don't mark files
		// that couldn't actually be read.
		if rt != nil {
			rt.MarkRead(cwd, p.Path)
		}

		if !utf8.Valid(data) {
			return Result{Content: "file appears to be binary", IsError: true}, nil
		}

		content := string(data)
		lines := strings.Split(content, "\n")
		totalLines := len(lines)

		// For a large source file with no explicit range requested, an index of
		// the whole file beats its first page. Returned before the served-range
		// bookkeeping below, because the model has not been shown any of these
		// lines and a later read of them must not be suppressed.
		if !rangeRequested && !p.Full {
			if outline := outlineFor(path, lines); outline != "" {
				return Result{Content: outline, Truncated: true, ResultBytes: len(data)}, nil
			}
		}

		// Apply offset (1-based).
		startLine := 1
		if p.Offset > 0 {
			startLine = p.Offset
		}
		if startLine > totalLines {
			return Result{Content: fmt.Sprintf("offset %d exceeds file length (%d lines)", startLine, totalLines), IsError: true}, nil
		}

		// Apply limit.
		endLine := totalLines
		if p.Limit > 0 && startLine+p.Limit-1 < endLine {
			endLine = startLine + p.Limit - 1
		}

		// Skip lines this session has already been shown from an unchanged
		// file. Repeated reads were 23.6% of all tool result tokens across
		// analysed sessions - one file was read 58 times in a single session.
		// full:true is the deliberate escape hatch for a model that has lost
		// the earlier content (compaction, handoff) and needs it resent.
		requestedStart := startLine
		if rt != nil && !p.Full {
			novelStart, novelEnd, hasNovel := rt.Novel(cwd, p.Path, FileIdentity(info), startLine, endLine)
			if !hasNovel {
				return Result{Content: fmt.Sprintf(
					"[lines %d-%d of %s are unchanged since you last read them; nothing new to show. Set full:true to have the content resent.]",
					startLine, endLine, p.Path,
				)}, nil
			}
			startLine, endLine = novelStart, novelEnd
		}

		selected := strings.Join(lines[startLine-1:endLine], "\n")
		tr := TruncateHeadRaw(selected, DefaultMaxLines, DefaultMaxBytes)
		output := tr.Content

		if startLine > requestedStart {
			output = fmt.Sprintf(
				"[lines %d-%d unchanged since you last read them; showing from line %d.]\n\n",
				requestedStart, startLine-1, startLine,
			) + output
		}

		switch {
		case tr.Truncated && tr.OutputLines == 0:
			// The first requested line alone exceeds the byte limit. Point the
			// model at a shell fallback that can slice within the line.
			lineSize := FormatSize(len(lines[startLine-1]))
			output = fmt.Sprintf(
				"[line %d is %s, exceeding the %s output limit. Use shell: sed -n '%dp' %s | head -c %d]",
				startLine, lineSize, FormatSize(DefaultMaxBytes), startLine, p.Path, DefaultMaxBytes,
			)
		case tr.Truncated:
			shownEnd := startLine + tr.OutputLines - 1
			output += fmt.Sprintf(
				"\n\n[showing lines %d-%d of %d. Use offset=%d to continue.]",
				startLine, shownEnd, totalLines, shownEnd+1,
			)
		case endLine < totalLines:
			// A user-specified limit stopped early, but the file has more content.
			output += fmt.Sprintf(
				"\n\n[%d more lines in file. Use offset=%d to continue.]",
				totalLines-endLine, endLine+1,
			)
		}

		return Result{Content: output, Truncated: tr.Truncated || endLine < totalLines, ResultBytes: tr.OriginalSize}, nil
	}
}
