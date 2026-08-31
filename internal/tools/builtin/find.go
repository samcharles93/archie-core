// Lifted from tau internal/agent/tools/find.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//   - discovery is confined to the active workspace. Module-cache and other
//     absolute paths are rejected so find cannot list home or pkg/mod trees.
//   - runFindGoFallback split into compileFindFilter and findWalkVisitor to
//     satisfy golangci-lint's gocognit limit. No behavioural change.
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// FindParams are the parameters for the find tool.
type FindParams struct {
	Path     string `json:"path,omitempty"`      // directory to search in
	Pattern  string `json:"pattern,omitempty"`   // glob pattern for file names
	Type     string `json:"type,omitempty"`      // "file", "directory", or empty for both
	MaxDepth int    `json:"max_depth,omitempty"` // max directory depth (0 = unlimited)
	Exclude  string `json:"exclude,omitempty"`   // glob pattern to exclude (e.g. 'node_modules', '*.test.*')
}

var findSchema = Schema{
	Name:        "find",
	Description: fmt.Sprintf("Find files and directories by name pattern, or list a directory's contents. Respects .gitignore when fd is available. Returns a list of matching paths, truncated to %d results or %s (whichever is hit first). Omit pattern and set max_depth:1 to list a single directory. Use exclude to skip unwanted directories.", DefaultMaxLines, FormatSize(DefaultMaxBytes)),
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory to search in. Defaults to the active workspace. Paths outside that workspace are rejected."
			},
			"pattern": {
				"type": "string",
				"description": "Glob pattern to match file/directory names (e.g. '*.go', 'README*')"
			},
			"type": {
				"type": "string",
				"enum": ["file", "directory"],
				"description": "Filter by type: 'file' or 'directory'. Omit for both."
			},
			"max_depth": {
				"type": "integer",
				"description": "Maximum directory depth to descend. 0 means unlimited. Use to limit recursion in large trees."
			},
			"exclude": {
				"type": "string",
				"description": "Glob pattern for directories or files to exclude (e.g. 'node_modules', '*.test.go', 'vendor')."
			}
		}
	}`),
}

// NewFindTool creates the built-in find tool.
func NewFindTool(cwd string) Tool {
	return Tool{
		Schema:  findSchema,
		Source:  "builtin",
		Execute: makeFindExecutor(cwd),
	}
}

func makeFindExecutor(cwd string) Executor {
	return func(ctx context.Context, params json.RawMessage, _ UIBridge) (Result, error) {
		var p FindParams
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{Content: fmt.Sprintf("invalid parameters: %v", err), IsError: true}, nil
		}

		ctx, cancel := context.WithTimeout(ctx, DefaultToolTimeout)
		defer cancel()

		searchPath := cwd
		if p.Path != "" {
			searchPath = resolvePath(cwd, p.Path)
		}

		if !isConfined(cwd, searchPath) {
			return Result{Content: "error: path escapes working directory", IsError: true, ErrorKind: "sandbox_escape"}, nil
		}

		info, err := os.Stat(searchPath)
		if err != nil {
			return Result{Content: fmt.Sprintf("error accessing path: %v", err), IsError: true}, nil
		}
		if !info.IsDir() {
			return Result{Content: "path must be a directory", IsError: true}, nil
		}

		binary, err := findBinary()
		if err != nil {
			// No external binary available - use pure-Go fallback.
			return runFindGoFallback(ctx, cwd, searchPath, p)
		}
		args := buildFindArgs(binary, p, searchPath)

		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = cwd

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()

		output := strings.TrimSpace(stdout.String())
		if output == "" && err != nil {
			errMsg := stderr.String()
			if errMsg == "" {
				errMsg = err.Error()
			}
			return Result{Content: fmt.Sprintf("find error: %s", errMsg), IsError: true}, nil
		}

		if output == "" {
			return Result{Content: "no matches found"}, nil
		}

		tr := TruncateHead(output, DefaultMaxLines, DefaultMaxBytes)
		return Result{Content: tr.Content, Truncated: tr.Truncated, ResultBytes: tr.OriginalSize}, nil
	}
}

func runFindGoFallback(ctx context.Context, cwd, searchPath string, p FindParams) (Result, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		return Result{Content: fmt.Sprintf("error accessing path: %v", err), IsError: true}, nil
	}
	if !info.IsDir() {
		return Result{Content: "path must be a directory", IsError: true}, nil
	}

	var matches []string

	patternRe, err := compileFindFilter(p.Pattern, true)
	if err != nil {
		return Result{Content: fmt.Sprintf("invalid pattern: %v", err), IsError: true}, nil
	}
	excludeRe, err := compileFindFilter(p.Exclude, false)
	if err != nil {
		return Result{Content: fmt.Sprintf("invalid exclude: %v", err), IsError: true}, nil
	}

	visit := findWalkVisitor(ctx, cwd, searchPath, p, patternRe, excludeRe, &matches)
	err = filepath.WalkDir(searchPath, visit)
	if err != nil {
		return Result{Content: fmt.Sprintf("find error: %v", err), IsError: true}, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{Content: fmt.Sprintf("find error: search did not complete: %v", ctxErr), IsError: true}, nil
	}

	if len(matches) == 0 {
		return Result{Content: "no matches found"}, nil
	}

	sort.Strings(matches)
	output := strings.Join(matches, "\n")
	tr := TruncateHead(output, DefaultMaxLines, DefaultMaxBytes)
	return Result{Content: tr.Content, Truncated: tr.Truncated, ResultBytes: tr.OriginalSize}, nil
}

// compileFindFilter compiles a glob filter into a regexp. anchored wraps the
// translated pattern in ^(?:...)$ (whole-name match, used for the include
// pattern); unanchored leaves it as (?:...) (substring match, used for
// exclude). Returns a nil regexp and nil error for an empty glob.
func compileFindFilter(glob string, anchored bool) (*regexp.Regexp, error) {
	if glob == "" {
		return nil, nil
	}
	reStr := globToRegex(glob)
	if anchored {
		return regexp.Compile("^(?:" + reStr + ")$")
	}
	return regexp.Compile("(?:" + reStr + ")")
}

// findWalkVisitor returns the filepath.WalkDir callback for
// runFindGoFallback: it enforces max depth, skips hidden entries and
// exclude matches, applies the type and pattern filters, and appends
// surviving paths (relative to cwd) to matches.
func findWalkVisitor(
	ctx context.Context, cwd, searchPath string, p FindParams, patternRe, excludeRe *regexp.Regexp, matches *[]string,
) fs.WalkDirFunc {
	return func(walkPath string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // intentionally skip inaccessible entries
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Calculate depth relative to searchPath.
		if p.MaxDepth > 0 && walkPath != searchPath {
			relDepth := strings.Count(strings.TrimPrefix(walkPath, searchPath+string(filepath.Separator)), string(filepath.Separator))
			if relDepth >= p.MaxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		base := d.Name()
		// Skip hidden entries like .git, .env, etc.
		if strings.HasPrefix(base, ".") && walkPath != searchPath {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Apply exclude filter to both files and directories.
		if excludeRe != nil {
			if excludeRe.MatchString(base) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip root directory itself.
		if walkPath == searchPath {
			return nil
		}

		// Type filter.
		switch p.Type {
		case "file":
			if d.IsDir() {
				return nil
			}
		case "directory":
			if !d.IsDir() {
				return nil
			}
		}

		// Pattern filter.
		if patternRe != nil {
			if !patternRe.MatchString(base) {
				return nil
			}
		}

		// Use path relative to cwd for output consistency.
		outputPath, err := filepath.Rel(cwd, walkPath)
		if err != nil {
			outputPath = walkPath
		}
		*matches = append(*matches, filepath.ToSlash(outputPath))
		return nil
	}
}

func isFdBinary(binary string) bool {
	base := filepath.Base(binary)
	base = strings.TrimSuffix(base, filepath.Ext(base)) // strip .exe on Windows
	return base == "fd" || base == "fd-find" || base == "fdfind"
}

func buildFindArgs(binary string, p FindParams, searchPath string) []string {
	// fd (fd-find) style
	if isFdBinary(binary) {
		args := []string{}
		if p.Pattern != "" {
			args = append(args, "--glob", p.Pattern)
		}
		switch p.Type {
		case "file":
			args = append(args, "--type", "f")
		case "directory":
			args = append(args, "--type", "d")
		}
		if p.MaxDepth > 0 {
			args = append(args, "--max-depth", fmt.Sprintf("%d", p.MaxDepth))
		}
		if p.Exclude != "" {
			args = append(args, "--exclude", p.Exclude)
		}
		args = append(args, "--search-path", searchPath)
		return args
	}

	// POSIX find style
	args := []string{searchPath}
	switch p.Type {
	case "file":
		args = append(args, "-type", "f")
	case "directory":
		args = append(args, "-type", "d")
	}
	if p.MaxDepth > 0 {
		args = append(args, "-maxdepth", fmt.Sprintf("%d", p.MaxDepth))
	}
	if p.Exclude != "" {
		args = append(args, "-not", "-path", fmt.Sprintf("*/%s/*", p.Exclude))
	}
	if p.Pattern != "" {
		args = append(args, "-name", p.Pattern)
	}
	return args
}

func findBinary() (string, error) {
	// Prefer fd; fall back to POSIX find on Unix only.
	for _, name := range []string{"fd", "fdfind", "fd-find"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("fd is required on Windows but was not found in PATH")
	}
	return "find", nil
}

// globToRegex converts a simple glob pattern to a regex string.
// Supports * (any chars), ? (single char), and character classes [...].
func globToRegex(pattern string) string {
	var sb strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if inClass {
				sb.WriteByte(c)
			} else {
				sb.WriteString(".*")
			}
		case '?':
			if inClass {
				sb.WriteByte(c)
			} else {
				sb.WriteByte('.')
			}
		case '[':
			inClass = true
			sb.WriteByte(c)
		case ']':
			inClass = false
			sb.WriteByte(c)
		case '\\':
			if i+1 < len(pattern) {
				sb.WriteByte(pattern[i+1])
				i++
			} else {
				sb.WriteByte(c)
			}
		case '.', '+', '^', '$', '(', ')', '|':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}
