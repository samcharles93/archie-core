// html-gate validates HTML files in the worktree for structural issues.
// It catches unclosed tags, mismatched tag pairs, orphaned closing tags,
// and missing closing divs that break grid layouts.
//
// Exports: func Gate() gate.Finding
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/samcharles93/archie-core/internal/gate"
)

// selfClosing are HTML elements that never have a closing tag.
var selfClosing = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

// tagRE matches HTML tags: <tagname ...> or </tagname>
var tagRE = regexp.MustCompile(`</?([a-zA-Z][a-zA-Z0-9]*)\b[^>]*/?>`)

// divCloseRE matches closing </div> tags — used to detect structural
// problems that break CSS grid layouts.
var divOpenRE = regexp.MustCompile(`<div\b[^>]*>`)

// Gate validates HTML files in the worktree. Called during the gate
// stage after Go checks pass.
func Gate(ctx gate.Context) []gate.Finding {
	var findings []gate.Finding

	for _, f := range ctx.ChangedFiles {
		ext := strings.ToLower(filepath.Ext(f))
		if ext != ".html" && ext != ".htm" {
			continue
		}
		findings = append(findings, checkHTML(filepath.Join(ctx.Dir, f), f)...)
	}

	return findings
}

func checkHTML(path, relPath string) []gate.Finding {
	var findings []gate.Finding

	data, err := os.ReadFile(path)
	if err != nil {
		return []gate.Finding{{
			Level:   "warn",
			File:    relPath,
			Message: fmt.Sprintf("cannot read HTML file: %v", err),
		}}
	}

	file := string(data)
	lines := strings.Split(file, "\n")

	// Tag stack for mismatch detection.
	type tagEntry struct {
		name string
		line int
	}
	var stack []tagEntry

	tags := tagRE.FindAllStringSubmatch(file, -1)
	tagIndexes := tagRE.FindAllStringIndex(file, -1)

	for i, match := range tags {
		if len(match) < 2 {
			continue
		}
		fullTag := match[0]
		name := strings.ToLower(match[1])

		// Self-closing or void element: skip.
		if selfClosing[name] {
			continue
		}
		// Self-closing syntax: <br/> or <img ... />
		if strings.HasSuffix(strings.TrimRight(fullTag, " "), "/>") {
			continue
		}

		// Find the line number for this tag.
		offset := tagIndexes[i][0]
		lineNum := lineForOffset(lines, offset)

		isClosing := strings.HasPrefix(fullTag, "</")

		if isClosing {
			// Closing tag with empty stack: orphaned close.
			if len(stack) == 0 {
				findings = append(findings, gate.Finding{
					Level:   "error",
					File:    relPath,
					Line:    lineNum,
					Message: fmt.Sprintf("orphaned closing tag </%s> (no matching open tag)", name),
				})
				continue
			}
			top := stack[len(stack)-1]
			if top.name != name {
				findings = append(findings, gate.Finding{
					Level:   "error",
					File:    relPath,
					Line:    lineNum,
					Message: fmt.Sprintf("mismatched tag: </%s> closes <%s> (opened line %d)", name, top.name, top.line),
				})
				// Pop until we find the match or exhaust the stack.
				for len(stack) > 0 && stack[len(stack)-1].name != name {
					stack = stack[:len(stack)-1]
				}
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				continue
			}
			// Matching close: pop.
			stack = stack[:len(stack)-1]
		} else {
			// Opening tag: push.
			stack = append(stack, tagEntry{name: name, line: lineNum})
		}
	}

	// Unclosed tags at EOF.
	for _, entry := range stack {
		findings = append(findings, gate.Finding{
			Level:   "error",
			File:    relPath,
			Line:    entry.line,
			Message: fmt.Sprintf("unclosed <%s> tag (opened line %d)", entry.name, entry.line),
		})
	}

	// Structural check: count <div> and </div> separately.
	// A mismatch here breaks grid layouts silently.
	divOpens := len(divOpenRE.FindAllString(file, -1))
	divCloses := strings.Count(file, "</div>")
	if divOpens != divCloses {
		findings = append(findings, gate.Finding{
			Level:   "error",
			File:    relPath,
			Message: fmt.Sprintf("mismatched <div> tags: %d opens, %d closes", divOpens, divCloses),
		})
	}

	// Check for common anti-patterns.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Empty href (navigates to self — usually a mistake).
		if strings.Contains(trimmed, `href=""`) {
			findings = append(findings, gate.Finding{
				Level:   "warn",
				File:    relPath,
				Line:    i + 1,
				Message: "empty href=\"\" attribute (navigates to self — use \"#\" or remove the link)",
			})
		}

		// Inline style with !important (blocks responsive overrides).
		if strings.Contains(trimmed, "!important") && strings.Contains(trimmed, "style=") {
			findings = append(findings, gate.Finding{
				Level:   "warn",
				File:    relPath,
				Line:    i + 1,
				Message: "!important in inline style — blocks responsive overrides from CSS media queries",
			})
		}
	}

	return findings
}

// lineForOffset returns the 1-based line number for a byte offset.
func lineForOffset(lines []string, offset int) int {
	pos := 0
	for i, line := range lines {
		next := pos + len(line) + 1 // +1 for newline
		if offset < next {
			return i + 1
		}
		pos = next
	}
	return len(lines)
}
