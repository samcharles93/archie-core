// Lifted from tau internal/agent/tools/edit.go
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
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// EditParams are the parameters for the edit tool.
type EditParams struct {
	Path  string       `json:"path"`
	Edits []EditAction `json:"edits"`
}

// EditAction is a single search-and-replace operation.
type EditAction struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

var editSchema = Schema{
	Name:        "edit",
	Description: "Edit an existing file using exact text replacement. Each edit specifies old_text (must match exactly including whitespace) and new_text. Every old_text is matched against the original file and must be unique in it; set replace_all to true to replace every occurrence instead. Edits must not overlap - merge nearby changes into one edit. All edits are validated first and applied atomically: on any failure the file is left unchanged.",
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute or relative file path to edit"
			},
			"edits": {
				"type": "array",
				"description": "One or more search-and-replace operations, each matched against the original file. Keep old_text as small as possible while still unique.",
				"items": {
					"type": "object",
					"properties": {
						"old_text": {
							"type": "string",
							"description": "Exact text to find (must match exactly including whitespace and be unique in the file unless replace_all is set)"
						},
						"new_text": {
							"type": "string",
							"description": "Replacement text"
						},
						"replace_all": {
							"type": "boolean",
							"description": "Replace all occurrences of old_text. Defaults to false (old_text must be unique)."
						}
					},
					"required": ["old_text", "new_text"]
				}
			}
		},
		"required": ["path", "edits"]
	}`),
}

// NewEditTool creates the built-in edit tool.
func NewEditTool(cwd string, mq *MutationQueue, rt *ReadTracker) Tool {
	return Tool{
		Schema:  editSchema,
		Source:  "builtin",
		Execute: makeEditExecutor(cwd, mq, rt),
	}
}

// parseEditParams unmarshals edit parameters, tolerating two model quirks
// observed in the wild: sending edits as a JSON-encoded string instead of an
// array, and sending a single flat old_text/new_text pair at the top level
// instead of inside edits[].
func parseEditParams(params json.RawMessage) (EditParams, error) {
	var wire struct {
		Path       string          `json:"path"`
		Edits      json.RawMessage `json:"edits"`
		OldText    *string         `json:"old_text"`
		NewText    *string         `json:"new_text"`
		ReplaceAll bool            `json:"replace_all"`
	}
	if err := json.Unmarshal(params, &wire); err != nil {
		return EditParams{}, err
	}

	p := EditParams{Path: wire.Path}

	if len(wire.Edits) > 0 {
		raw := wire.Edits
		// Some models double-encode the array as a JSON string.
		var asString string
		if err := json.Unmarshal(raw, &asString); err == nil {
			raw = json.RawMessage(asString)
		}
		if err := json.Unmarshal(raw, &p.Edits); err != nil {
			return EditParams{}, fmt.Errorf("invalid edits: %w", err)
		}
	}

	// A flat old_text/new_text pair at the top level becomes a single edit.
	if wire.OldText != nil && wire.NewText != nil {
		p.Edits = append(p.Edits, EditAction{
			OldText:    *wire.OldText,
			NewText:    *wire.NewText,
			ReplaceAll: wire.ReplaceAll,
		})
	}

	return p, nil
}

func makeEditExecutor(cwd string, mq *MutationQueue, rt *ReadTracker) Executor {
	return func(ctx context.Context, params json.RawMessage, _ UIBridge) (Result, error) {
		p, err := parseEditParams(params)
		if err != nil {
			return Result{Content: fmt.Sprintf("invalid parameters: %v", err), IsError: true}, nil
		}

		if len(p.Edits) == 0 {
			return Result{Content: "at least one edit is required", IsError: true}, nil
		}

		_, cancel := context.WithTimeout(ctx, DefaultToolTimeout)
		defer cancel()

		path := resolvePath(cwd, p.Path)

		if !isConfined(cwd, path) {
			return Result{Content: "path escapes working directory", IsError: true}, nil
		}

		// Enforce read-before-write check.
		if rt != nil {
			if err := rt.CheckRead(cwd, p.Path); err != nil {
				return Result{Content: err.Error(), IsError: true}, nil
			}
		}

		// Check file size before reading to avoid OOM on large files.
		info, err := os.Stat(path)
		if err != nil {
			return Result{Content: fmt.Sprintf("error stating file: %v", err), IsError: true}, nil
		}
		if info.Size() > maxReadBytes {
			return Result{Content: fmt.Sprintf("file too large to edit (%s > %s)", FormatSize(int(info.Size())), FormatSize(maxReadBytes)), IsError: true}, nil
		}

		release := mq.Acquire(path)
		defer release()

		data, err := os.ReadFile(path)
		if err != nil {
			return Result{Content: fmt.Sprintf("error reading file: %v", err), IsError: true}, nil
		}

		// Strip a UTF-8 BOM and normalise CRLF to LF before matching - the
		// model never includes an invisible BOM or carriage returns in
		// old_text. Both are restored on write.
		raw := string(data)
		bom := ""
		if strings.HasPrefix(raw, utf8BOM) {
			bom = utf8BOM
			raw = strings.TrimPrefix(raw, utf8BOM)
		}
		hadCRLF := strings.Contains(raw, "\r\n")
		content := strings.ReplaceAll(raw, "\r\n", "\n")

		newContent, err := applyEdits(content, p.Edits)
		if err != nil {
			errorKind := "invalid_arguments"
			if strings.Contains(err.Error(), "old_text not found") {
				errorKind = "stale_edit"
			}
			return Result{
				Content:   err.Error() + "; no edits were written; reread the smallest affected range before retrying",
				IsError:   true,
				ErrorKind: errorKind,
			}, nil
		}
		if newContent == content {
			return Result{Content: fmt.Sprintf("no changes made to %s: the edits produced identical content", path), IsError: true}, nil
		}

		if hadCRLF {
			newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
		}

		if err := writeFileAtomic(path, []byte(bom+newContent), 0o644); err != nil {
			return Result{Content: fmt.Sprintf("error writing file: %v", err), IsError: true}, nil
		}

		return Result{
			Content: fmt.Sprintf("applied %d edit(s) to %s", len(p.Edits), path),
			Details: DiffDetails{Path: path, OldContent: content, NewContent: newContent},
		}, nil
	}
}

const utf8BOM = "\ufeff"

// editSpan is a resolved replacement range in the original content.
type editSpan struct {
	start, end int    // byte offsets in the original content
	repl       string // replacement text
	editIdx    int    // index of the edit that produced this span (for errors)
}

// applyEdits resolves every edit against the ORIGINAL content, validates that
// the resulting replacement spans do not overlap, and applies them in one
// pass. It never partially applies: any error leaves the content untouched.
//
// Matching is exact first; if old_text is not found, a fuzzy pass retries the
// match with trailing whitespace stripped from every line of both the content
// and old_text, which recovers the most common exact-match failure.
func applyEdits(content string, edits []EditAction) (string, error) {
	var spans []editSpan

	for i, edit := range edits {
		if edit.OldText == "" {
			return "", fmt.Errorf("edit %d: old_text must not be empty", i+1)
		}
		if edit.OldText == edit.NewText {
			return "", fmt.Errorf("edit %d: old_text and new_text are identical", i+1)
		}

		matches := indexAll(content, edit.OldText)

		if edit.ReplaceAll {
			if len(matches) == 0 {
				matches = fuzzyIndexAll(content, edit.OldText)
			}
			if len(matches) == 0 {
				return "", editNotFoundError(i, content, edit.OldText)
			}
			for _, m := range matches {
				spans = append(spans, editSpan{start: m[0], end: m[1], repl: edit.NewText, editIdx: i})
			}
			continue
		}

		switch len(matches) {
		case 1:
			spans = append(spans, editSpan{start: matches[0][0], end: matches[0][1], repl: edit.NewText, editIdx: i})
		case 0:
			fuzzy := fuzzyIndexAll(content, edit.OldText)
			switch len(fuzzy) {
			case 1:
				spans = append(spans, editSpan{start: fuzzy[0][0], end: fuzzy[0][1], repl: edit.NewText, editIdx: i})
			case 0:
				return "", editNotFoundError(i, content, edit.OldText)
			default:
				return "", editAmbiguousError(i, len(fuzzy))
			}
		default:
			return "", editAmbiguousError(i, len(matches))
		}
	}

	sort.Slice(spans, func(a, b int) bool { return spans[a].start < spans[b].start })

	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return "", fmt.Errorf(
				"edits %d and %d overlap in the file; merge them into a single edit",
				spans[i-1].editIdx+1, spans[i].editIdx+1,
			)
		}
	}

	var b strings.Builder
	cursor := 0
	for _, s := range spans {
		b.WriteString(content[cursor:s.start])
		b.WriteString(s.repl)
		cursor = s.end
	}
	b.WriteString(content[cursor:])
	return b.String(), nil
}

func editNotFoundError(idx int, content, oldText string) error {
	base := fmt.Sprintf("edit %d: old_text not found in file; it must match exactly, including whitespace and indentation", idx+1)
	if hint := nearestMatchHint(content, oldText); hint != "" {
		return fmt.Errorf("%s\n%s", base, hint)
	}
	return errors.New(base)
}

// nearestMatchHint locates the most likely intended location for a failed
// old_text and quotes the file's actual text there, so the model can correct
// the edit from the error alone instead of spending a round trip re-reading
// the file. stale_edit was 6.7% of all edit calls across analysed sessions.
//
// The anchor is the first non-blank line of old_text compared with leading and
// trailing whitespace removed, because indentation drift is the dominant cause:
// the model reproduces the right code at the wrong nesting depth. Matching on
// content alone is deliberately loose - a wrong hint costs nothing beyond a few
// lines of output, whereas no hint costs a whole round trip.
func nearestMatchHint(content, oldText string) string {
	var anchor string
	wantLines := strings.Split(oldText, "\n")
	for _, l := range wantLines {
		if t := strings.TrimSpace(l); t != "" {
			anchor = t
			break
		}
	}
	if anchor == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != anchor {
			continue
		}
		end := min(i+len(wantLines), len(lines))
		return fmt.Sprintf(
			"nearest match is at line %d; the file actually contains:\n%s",
			i+1, strings.Join(lines[i:end], "\n"),
		)
	}
	return ""
}

func editAmbiguousError(idx, occurrences int) error {
	return fmt.Errorf("edit %d: old_text appears %d times in file; add surrounding context to make it unique, or set replace_all", idx+1, occurrences)
}

// indexAll returns the [start, end) offsets of every non-overlapping
// occurrence of needle in content.
func indexAll(content, needle string) [][2]int {
	var out [][2]int
	for from := 0; ; {
		i := strings.Index(content[from:], needle)
		if i < 0 {
			return out
		}
		start := from + i
		out = append(out, [2]int{start, start + len(needle)})
		from = start + len(needle)
	}
}

// fuzzyIndexAll matches needle against content with trailing whitespace
// stripped from every line of both, returning spans in the ORIGINAL content.
// Only trailing-whitespace differences are forgiven; everything else must
// still match exactly.
func fuzzyIndexAll(content, needle string) [][2]int {
	strippedNeedle := stripTrailingWhitespace(needle)
	if strippedNeedle == "" {
		return nil
	}

	strippedContent, offsets := buildStrippedIndex(content)

	var out [][2]int
	for _, m := range indexAll(strippedContent, strippedNeedle) {
		start := offsets[m[0]]
		end := len(content)
		if m[1] < len(offsets) {
			end = offsets[m[1]]
		}
		out = append(out, [2]int{start, end})
	}
	return out
}

func stripTrailingWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// buildStrippedIndex returns content with trailing whitespace stripped from
// every line, plus a per-character map from stripped offsets back to offsets
// in the original content.
func buildStrippedIndex(content string) (string, []int) {
	var b strings.Builder
	b.Grow(len(content))
	offsets := make([]int, 0, len(content))

	lines := strings.Split(content, "\n")
	pos := 0
	for i, line := range lines {
		stripped := strings.TrimRight(line, " \t")
		for j := range len(stripped) {
			offsets = append(offsets, pos+j)
		}
		b.WriteString(stripped)
		if i < len(lines)-1 {
			offsets = append(offsets, pos+len(line)) // the newline
			b.WriteByte('\n')
		}
		pos += len(line) + 1
	}
	return b.String(), offsets
}
