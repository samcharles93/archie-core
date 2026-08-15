// Lifted from tau internal/agent/tools/grep.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//   - the embedded ripgrep binaries (upstream internal/tools/builtin/rg) were
//     removed. They were 14MB of tracked binaries that were re-extracted to a
//     temporary directory on every run. grepBinary now resolves rg from PATH
//     and the existing pure-Go grepFallback covers its absence.
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// grepMaxLineChars caps each output line so a single long line (e.g. in
	// minified or generated files) cannot blow out the context window.
	grepMaxLineChars = 500

	// grepDefaultLimit is the default maximum number of matches returned.
	grepDefaultLimit = 100
	grepMaxBytes     = 24 * 1024

	// grepMaxContext bounds context_before/context_after. Large context values
	// spend the whole byte budget on padding: at the default limit of 100
	// matches, every extra context line is 100 extra lines of output, so a
	// request for 10 lines either side truncates long before it has shown 100
	// distinct matches. Clamping keeps the budget on matches instead.
	grepMaxContext = 5
)

// grepMatchLineRe recognises a match line in ripgrep-style output
// (path:line:content). Context lines use '-' separators instead.
var grepMatchLineRe = regexp.MustCompile(`^.+?:\d+:`)

// GrepParams are the parameters for the grep tool.
type GrepParams struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path,omitempty"`    // file or directory
	Include       string `json:"include,omitempty"` // glob pattern for file names
	Literal       bool   `json:"literal,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	ContextBefore int    `json:"context_before,omitempty"` // lines before each match (-B)
	ContextAfter  int    `json:"context_after,omitempty"`  // lines after each match (-A)
	Limit         int    `json:"limit,omitempty"`          // max matches to return
}

// GrepIndex provides conservative workspace-wide candidate files. The grep
// tool remains responsible for authoritative matching and output formatting.
type GrepIndex interface {
	Candidates(context.Context, string, bool, bool) ([]string, bool)
}

var grepSchema = Schema{
	Name:        "grep",
	Description: fmt.Sprintf("Search file contents for a regex pattern using ripgrep (rg). Respects .gitignore. Returns matching lines with file paths and line numbers. Supports alternation (e.g. 'foo|bar') and full regex syntax. Use context_before/context_after to show surrounding lines (capped at %d each). Output is capped at %d matches (adjustable via limit) and long lines are truncated to %d chars.", grepMaxContext, grepDefaultLimit, grepMaxLineChars),
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Regex pattern to search for (e.g. 'handleCancel|CancelChat'). Set literal:true to match the pattern as plain text instead."
			},
			"path": {
				"type": "string",
				"description": "File or directory to search in. Defaults to current directory."
			},
			"include": {
				"type": "string",
				"description": "Glob pattern for file inclusion (e.g. '*.go', '*.ts')"
			},
			"literal": {
				"type": "boolean",
				"description": "Treat pattern as literal text instead of a regex. Defaults to false."
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of matches to return. Defaults to 100."
			},
			"case_sensitive": {
				"type": "boolean",
				"description": "Case-sensitive search. Defaults to false (smart case)."
			},
			"context_before": {
				"type": "integer",
				"description": "Number of lines to show before each match (-B). Useful for seeing context without a follow-up read."
			},
			"context_after": {
				"type": "integer",
				"description": "Number of lines to show after each match (-A). Useful for seeing context without a follow-up read."
			}
		},
		"required": ["pattern"]
	}`),
}

// NewGrepTool creates the built-in grep tool.

func NewGrepTool(cwd string, indexes ...GrepIndex) Tool {
	var workspaceIndex GrepIndex
	if len(indexes) > 0 {
		workspaceIndex = indexes[0]
	}
	return Tool{
		Schema:  grepSchema,
		Source:  "builtin",
		Execute: makeGrepExecutor(cwd, workspaceIndex),
	}
}

func makeGrepExecutor(cwd string, workspaceIndex GrepIndex) Executor {
	return func(ctx context.Context, params json.RawMessage, _ UIBridge) (Result, error) {
		var p GrepParams
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{Content: fmt.Sprintf("invalid parameters: %v", err), IsError: true}, nil
		}

		if strings.TrimSpace(p.Pattern) == "" {
			return Result{Content: "pattern is required", IsError: true}, nil
		}

		ctx, cancel := context.WithTimeout(ctx, DefaultToolTimeout)
		defer cancel()

		clamped := clampGrepContext(&p)

		searchPath := cwd
		if p.Path != "" {
			searchPath = resolvePath(cwd, p.Path)
		}

		if !isReadConfined(cwd, searchPath) {
			return Result{Content: "error: path escapes working directory", IsError: true, ErrorKind: "sandbox_escape"}, nil
		}

		args := buildGrepArgs(p)
		searchTargets := []string{searchPath}
		searchBackend := "direct"
		// The index is workspace-wide, so it can serve any search scoped at or
		// below cwd once its candidates are narrowed to the requested subtree.
		// Requiring searchPath == cwd meant subdirectory searches silently fell
		// back to a full walk, which is why the index served only 5% of
		// searches across analysed sessions.
		if workspaceIndex != nil && isConfined(cwd, searchPath) {
			if candidates, ok := workspaceIndex.Candidates(ctx, p.Pattern, p.Literal, p.CaseSensitive); ok {
				if scoped := filterUnder(searchPath, candidates); len(scoped) > 0 {
					searchTargets = scoped
					searchBackend = "codesearch"
				}
				// An empty scoped set is NOT evidence of no matches: the index
				// may simply be cold or stale for this subtree, so fall through
				// to the authoritative direct walk rather than reporting "no
				// matches found" from an empty candidate list.
			}
		}
		args = append(args, searchTargets...)

		limit := p.Limit
		if limit <= 0 {
			limit = grepDefaultLimit
		}

		binary, err := grepBinary()
		if err != nil {
			searchBackend = "direct"
			// No external binary available - use pure-Go fallback.
			output, err := grepFallback(ctx, p, searchPath, cwd, searchTargets)
			if err != nil {
				return grepBackendResult(Result{Content: fmt.Sprintf("grep error: %v", err), IsError: true}, searchBackend, clamped), nil
			}
			if output == "" {
				return grepBackendResult(Result{Content: "no matches found"}, searchBackend, clamped), nil
			}
			return grepBackendResult(capGrepResult(output, limit), searchBackend, clamped), nil
		}

		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = cwd

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()

		output := stdout.String()
		if searchBackend == "codesearch" && err != nil {
			// Snapshot candidates can disappear, or an unusually large candidate
			// argv can fail to spawn. Retry the authoritative workspace path.
			searchBackend = "direct"
			args = append(buildGrepArgs(p), searchPath)
			cmd = exec.CommandContext(ctx, binary, args...)
			cmd.Dir = cwd
			stdout.Reset()
			stderr.Reset()
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
			output = stdout.String()
		}
		if output == "" && err != nil {
			// Exit 1 means "no matches"; exit 2 means a real failure (bad
			// pattern, unreadable path). Reporting the latter as an empty
			// result would have the model state a file contains nothing when
			// the search never ran, so the code must be checked, not just the
			// error type.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return grepBackendResult(Result{Content: "no matches found"}, searchBackend, clamped), nil
			}
			errMsg := stderr.String()
			if errMsg == "" {
				errMsg = err.Error()
			}
			return grepBackendResult(Result{Content: fmt.Sprintf("grep error: %s", errMsg), IsError: true}, searchBackend, clamped), nil
		}

		return grepBackendResult(capGrepResult(output, limit), searchBackend, clamped), nil
	}
}

// filterUnder returns the candidates at or below root. Paths are compared
// after cleaning so that a candidate is only kept when root is a genuine path
// prefix, not merely a string prefix ("/a/bc" is not under "/a/b").
func filterUnder(root string, candidates []string) []string {
	root = filepath.Clean(root)

	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if isConfined(root, filepath.Clean(c)) {
			out = append(out, c)
		}
	}
	return out
}

// clampGrepContext bounds the context window in place and returns a notice
// describing what was clamped, or "" if the request was already within limits.
func clampGrepContext(p *GrepParams) string {
	var clamped []string
	if p.ContextBefore > grepMaxContext {
		p.ContextBefore = grepMaxContext
		clamped = append(clamped, "context_before")
	}
	if p.ContextAfter > grepMaxContext {
		p.ContextAfter = grepMaxContext
		clamped = append(clamped, "context_after")
	}
	if len(clamped) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"[%s clamped to %d; for a wider window, read the file at the reported line numbers]",
		strings.Join(clamped, " and "), grepMaxContext,
	)
}

func grepBackendResult(result Result, backend, clampNotice string) Result {
	result.MetricLabels = map[string]string{"search_backend": backend}
	if clampNotice != "" && !result.IsError {
		result.Content += "\n" + clampNotice
	}
	return result
}

func capGrepResult(output string, limit int) Result {
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	matches := 0
	limitHit := false
	linesTruncated := false

	for _, line := range lines {
		if grepMatchLineRe.MatchString(line) {
			if matches >= limit {
				limitHit = true
				break
			}
			matches++
		}
		if len(line) > grepMaxLineChars {
			line = line[:truncationBoundary(line, grepMaxLineChars)] + "... [truncated]"
			linesTruncated = true
		}
		kept = append(kept, line)
	}

	tr := TruncateHead(strings.Join(kept, "\n"), DefaultMaxLines, grepMaxBytes)
	content := tr.Content
	if limitHit {
		content += fmt.Sprintf("\n\n[showing first %d matches; refine the pattern or raise limit]", limit)
	}
	if linesTruncated {
		content += fmt.Sprintf("\n[some lines truncated to %d chars]", grepMaxLineChars)
	}
	if tr.Truncated {
		content += fmt.Sprintf("\n[output truncated at %s; refine the pattern, search a narrower path, or use jq for structured JSON]", FormatSize(grepMaxBytes))
	}
	return Result{Content: content, Truncated: tr.Truncated || limitHit || linesTruncated, ResultBytes: len(output)}
}

// truncationBoundary returns the largest cut point <= max that does not split
// a UTF-8 rune.
func truncationBoundary(s string, limit int) int {
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

// grepFallback performs a pure-Go file scan for when ripgrep is not available.
// Works on all platforms including Windows. When targets has explicit file paths
// (not just the searchPath directory), only those files are searched.
func grepFallback(ctx context.Context, p GrepParams, searchPath, cwd string, targets []string) (string, error) {
	matcher, err := buildMatcher(p)
	if err != nil {
		return "", err
	}

	var results []grepResult

	// When explicit files are provided (index candidates), search only those.
	if len(targets) > 0 && targets[0] != searchPath {
		for _, path := range targets {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
			res, fErr := grepFile(ctx, path, matcher, p.ContextBefore, p.ContextAfter)
			if fErr != nil {
				continue
			}
			for i := range res {
				res[i].relPath, _ = filepath.Rel(cwd, path)
				res[i].relPath = filepath.ToSlash(res[i].relPath)
				if res[i].relPath == "" || res[i].relPath == "." {
					res[i].relPath = filepath.ToSlash(filepath.Base(path))
				}
			}
			results = append(results, res...)
		}
		return formatGrepResults(results), nil
	}

	info, err := os.Stat(searchPath)
	if err != nil {
		return "", err
	}

	if !info.IsDir() {
		res, err := grepFile(ctx, searchPath, matcher, p.ContextBefore, p.ContextAfter)
		if err != nil {
			return "", err
		}
		for i := range res {
			res[i].relPath, _ = filepath.Rel(cwd, searchPath)
			if res[i].relPath == "" || res[i].relPath == "." {
				res[i].relPath = filepath.Base(searchPath)
			}
		}
		results = append(results, res...)
	} else {
		err = filepath.WalkDir(searchPath, func(walkPath string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip inaccessible
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				// Skip hidden directories.
				if d.Name() != "" && d.Name()[0] == '.' && walkPath != searchPath {
					return filepath.SkipDir
				}
				return nil
			}

			// Skip hidden files.
			if d.Name() != "" && d.Name()[0] == '.' {
				return nil
			}

			// Include filter.
			if p.Include != "" {
				if matched, _ := filepath.Match(p.Include, d.Name()); !matched {
					return nil
				}
			}

			res, err := grepFile(ctx, walkPath, matcher, p.ContextBefore, p.ContextAfter)
			if err != nil {
				return nil // skip files we can't read
			}
			for i := range res {
				res[i].relPath, _ = filepath.Rel(cwd, walkPath)
				res[i].relPath = filepath.ToSlash(res[i].relPath)
				if res[i].relPath == "" || res[i].relPath == "." {
					res[i].relPath = filepath.ToSlash(filepath.Base(walkPath))
				}
			}
			results = append(results, res...)
			return nil
		})
		if err != nil && !errors.Is(err, ctx.Err()) {
			return "", err
		}
	}

	return formatGrepResults(results), nil
}

// buildMatcher returns a line matcher based on grep parameters.
func buildMatcher(p GrepParams) (func(string) bool, error) {
	if !p.Literal {
		var re *regexp.Regexp
		var err error
		if p.CaseSensitive || hasUppercase(p.Pattern) {
			re, err = regexp.Compile(p.Pattern)
		} else {
			re, err = regexp.Compile("(?i:" + p.Pattern + ")")
		}
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		return func(line string) bool { return re.MatchString(line) }, nil
	}
	if p.CaseSensitive || hasUppercase(p.Pattern) {
		return func(line string) bool { return strings.Contains(line, p.Pattern) }, nil
	}
	lowerPattern := strings.ToLower(p.Pattern)
	return func(line string) bool { return strings.Contains(strings.ToLower(line), lowerPattern) }, nil
}

// formatGrepResults deduplicates and formats grep results in ripgrep style.
func formatGrepResults(results []grepResult) string {
	if len(results) == 0 {
		return ""
	}

	// Overlapping context windows can emit the same line twice, and a line
	// can be both a match and a neighbour's context. Dedup by file+line,
	// preferring the match form.
	seen := make(map[string]int)
	deduped := results[:0]
	for _, r := range results {
		key := fmt.Sprintf("%s:%d", r.relPath, r.lineNum)
		if idx, ok := seen[key]; ok {
			if !r.isContext {
				deduped[idx].isContext = false
			}
			continue
		}
		seen[key] = len(deduped)
		deduped = append(deduped, r)
	}

	// Format results like ripgrep: path:line:content for matches,
	// path-line-content for context lines.
	var lines []string
	for _, r := range deduped {
		if r.isContext {
			lines = append(lines, fmt.Sprintf("%s-%d-%s", r.relPath, r.lineNum, r.content))
		} else {
			lines = append(lines, fmt.Sprintf("%s:%d:%s", r.relPath, r.lineNum, r.content))
		}
	}

	return strings.Join(lines, "\n")
}

type grepResult struct {
	relPath   string
	lineNum   int
	content   string
	isContext bool
}

func grepFile(ctx context.Context, path string, matcher func(string) bool, ctxBefore, ctxAfter int) ([]grepResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	str := string(data)
	allLines := strings.Split(str, "\n")

	var results []grepResult
	for i, line := range allLines {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if matcher(line) {
			// Add context lines before.
			start := max(i-ctxBefore, 0)
			for j := start; j < i; j++ {
				results = append(results, grepResult{
					lineNum:   j + 1,
					content:   allLines[j],
					isContext: true,
				})
			}

			// Add match line.
			results = append(results, grepResult{
				lineNum:   i + 1,
				content:   line,
				isContext: false,
			})

			// Add context lines after.
			end := i + ctxAfter
			if end >= len(allLines) {
				end = len(allLines) - 1
			}
			for j := i + 1; j <= end; j++ {
				results = append(results, grepResult{
					lineNum:   j + 1,
					content:   allLines[j],
					isContext: true,
				})
			}
		}
	}

	return results, nil
}

func buildGrepArgs(p GrepParams) []string {
	args := []string{"--line-number", "--with-filename", "--no-heading", "--color=never"}

	if p.Literal {
		args = append(args, "--fixed-strings")
	}

	if !p.CaseSensitive {
		args = append(args, "--smart-case")
	} else {
		args = append(args, "--case-sensitive")
	}

	if p.Include != "" {
		args = append(args, "--glob", p.Include)
	}

	if p.ContextBefore > 0 {
		args = append(args, fmt.Sprintf("-B%d", p.ContextBefore))
	}
	if p.ContextAfter > 0 {
		args = append(args, fmt.Sprintf("-A%d", p.ContextAfter))
	}

	args = append(args, "--", p.Pattern)
	return args
}

// grepBinary resolves ripgrep from PATH. When it is absent the caller falls
// back to grepFallback, the pure-Go search, so this returning an error is an
// ordinary outcome rather than a failure.
//
// Upstream tau embeds ripgrep binaries for each platform and extracts one to
// a temporary directory on demand. That cost 14MB of tracked binaries and
// wrote the binary out again on every run, which is why it was removed here.
func grepBinary() (string, error) {
	return exec.LookPath("rg")
}

// hasUppercase reports whether s contains any uppercase ASCII letter.
func hasUppercase(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}
