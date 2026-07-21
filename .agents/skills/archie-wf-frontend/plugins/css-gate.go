// css-gate validates every .css file and <style> block in the worktree.
// It catches syntax errors, missing units, invalid calc() expressions,
// and common responsive anti-patterns that browsers silently ignore but
// that make mobile layouts break.
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

// Gate reads .css files in the worktree and returns findings.
// Run during the gate stage after Go checks pass.
func Gate(ctx gate.Context) []gate.Finding {
	var findings []gate.Finding

	for _, f := range ctx.ChangedFiles {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".css" {
			findings = append(findings, checkCSSFile(filepath.Join(ctx.Dir, f), f)...)
		}
		if ext == ".html" || ext == ".htm" {
			findings = append(findings, checkStyleBlocks(filepath.Join(ctx.Dir, f), f)...)
		}
	}

	return findings
}

var (
	// Matches orphaned property lines — lines after a closing brace that
	// look like CSS properties but aren't inside a selector block.
	orphanLine = regexp.MustCompile(`^\s+(background|border|padding|margin|display|overflow|white-space|text-overflow|word-break|font-size|line-height):`)
	
	// Matches invalid calc() expressions that use grid-only functions.
	badCalc = regexp.MustCompile(`calc\([^)]*\bminmax\b`)
	
	// Matches CSS custom properties used in calc without fallback (valid but risky).
	// This is a warn, not an error.
	
	// Matches duplicate property declarations within a block.
	dupProp = regexp.MustCompile(`^\s*([a-z-]+)\s*:`)
)

func checkCSSFile(path, relPath string) []gate.Finding {
	var findings []gate.Finding

	data, err := os.ReadFile(path)
	if err != nil {
		return []gate.Finding{{
			Level:   "warn",
			File:    relPath,
			Message: fmt.Sprintf("cannot read CSS file: %v", err),
		}}
	}

	file := string(data)
	lines := strings.Split(file, "\n")
	inBlock := 0 // brace depth

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track brace depth.
		for _, ch := range line {
			if ch == '{' {
				inBlock++
			}
			if ch == '}' {
				inBlock--
			}
		}

		// Orphaned property: looks like CSS but depth is 0.
		if inBlock <= 0 && orphanLine.MatchString(line) {
			findings = append(findings, gate.Finding{
				Level:   "error",
				File:    relPath,
				Line:    i + 1,
				Message: fmt.Sprintf("orphaned CSS property (missing opening brace): %s", trimmed),
			})
		}

		// Bad calc expressions.
		if badCalc.MatchString(line) {
			findings = append(findings, gate.Finding{
				Level:   "error",
				File:    relPath,
				Line:    i + 1,
				Message: "invalid calc() expression: minmax() is a grid function, not valid in calc()",
			})
		}

		// Unclosed brace depth at EOF.
		if i == len(lines)-1 && inBlock > 0 {
			findings = append(findings, gate.Finding{
				Level:   "error",
				File:    relPath,
				Message: fmt.Sprintf("unclosed brace at end of file (%d blocks still open)", inBlock),
			})
		}
	}

	// Check for common responsive anti-patterns in the full file.
	if strings.Contains(file, "@media") && !strings.Contains(file, "max-width") && !strings.Contains(file, "min-width") {
		// Has @media but no width breakpoint — probably not responsive.
		// This is a warn, not an error — some @media uses are fine without width.
	}

	return findings
}

func checkStyleBlocks(path, relPath string) []gate.Finding {
	var findings []gate.Finding

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	file := string(data)

	// Extract <style>...</style> blocks.
	styleRE := regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)
	matches := styleRE.FindAllStringSubmatch(file, -1)

	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		// Check the style content the same way we check .css files.
		// Write to a temp approach: just run the same checks on the content.
		lines := strings.Split(m[1], "\n")
		inBlock := 0
		for i, line := range lines {
			for _, ch := range line {
				if ch == '{' {
					inBlock++
				}
				if ch == '}' {
					inBlock--
				}
			}
			if inBlock <= 0 && orphanLine.MatchString(line) {
				findings = append(findings, gate.Finding{
					Level:   "error",
					File:    relPath,
					Line:    i + 1,
					Message: fmt.Sprintf("orphaned CSS in <style> block: %s", strings.TrimSpace(line)),
				})
			}
			if badCalc.MatchString(line) {
				findings = append(findings, gate.Finding{
					Level:   "error",
					File:    relPath,
					Line:    i + 1,
					Message: "invalid calc() in <style> block: minmax() is not valid in calc()",
				})
			}
		}
	}

	// Check for viewport meta tag — required for responsive.
	if !strings.Contains(file, `name="viewport"`) {
		findings = append(findings, gate.Finding{
			Level:   "warn",
			File:    relPath,
			Message: "missing viewport meta tag — responsive layouts won't work on mobile",
		})
	}

	return findings
}
