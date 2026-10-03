package builtin

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// outlineThresholdLines is how much larger than the normal read budget a file
// must be before an outline replaces its first page. Files near the budget are
// better served raw: the outline saves little and costs a round trip to then
// fetch the body.
const outlineThresholdLines = 2 * DefaultReadLines

// minOutlineEntries guards against a "structural" summary that is really just
// a handful of arbitrary lines. Below this the raw head is more honest.
const minOutlineEntries = 3

// outlinePatterns maps a file extension to the pattern matching its top-level
// declarations. Only languages with a reliable, anchored declaration syntax are
// listed: for anything else an outline would be a guess presented as structure,
// so read falls back to raw content.
var outlinePatterns = map[string]*regexp.Regexp{
	".go":  regexp.MustCompile(`^(func|type|const|var|package)\s`),
	".ts":  tsOutlineRe(),
	".js":  tsOutlineRe(),
	".mjs": tsOutlineRe(),
	".tsx": tsOutlineRe(),
	".jsx": tsOutlineRe(),
	".py":  regexp.MustCompile(`^\s{0,4}(def|class|async def)\s`),
}

func tsOutlineRe() *regexp.Regexp {
	return regexp.MustCompile(`^(export\s|async\s+function\s|function\s|class\s|interface\s|type\s|const\s|enum\s)`)
}

// outlineFor returns a line-numbered index of a large source file's
// top-level declarations, or "".
func outlineFor(path string, lines []string) string {
	pattern, ok := outlinePatterns[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return ""
	}
	if len(lines) < outlineThresholdLines {
		return ""
	}

	var b strings.Builder
	entries := 0
	for i, l := range lines {
		if !pattern.MatchString(l) {
			continue
		}
		entries++
		fmt.Fprintf(&b, "%d: %s\n", i+1, strings.TrimRight(l, " \t{"))
	}

	// Generated tables and embedded data can be large with no declarations at
	// all; an almost-empty outline would hide the file rather than describe it.
	if entries < minOutlineEntries {
		return ""
	}

	return fmt.Sprintf(
		"[outline of %s (%d lines, %d declarations). Bodies omitted - read a range with offset/limit, or set full:true.]\n\n%s",
		filepath.Base(path), len(lines), entries, strings.TrimRight(b.String(), "\n"),
	)
}
