package prreview

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// EvidenceRequest is the finding an evidence package is extracted for: where it
// points and what it says.
type EvidenceRequest struct {
	File      string
	LineStart int
	LineEnd   int
	Title     string
	Body      string
	Evidence  string
}

// EvidencePackage is the ground truth a verifier checks a finding against: the
// code at the lines the finding cites, and the call sites of the identifiers it
// names. It is quoted code rather than a summary, because a verifier handed a
// summary is checking the reviewer's prose against itself.
type EvidencePackage struct {
	FindingTitle string
	File         string
	LineStart    int
	LineEnd      int
	// PrimaryCode is the cited lines and their context, each prefixed with its
	// line number in the file.
	PrimaryCode string
	// CallerSnippets are call sites elsewhere in the snapshot, each headed by
	// "path:line" and followed by the numbered lines around the call.
	CallerSnippets []string
}

const (
	// evidenceContextLines is how many lines each side of the cited range
	// PrimaryCode quotes.
	evidenceContextLines = 30
	// callerContextLines is the same for a call site: enough to read the call in
	// its function, not the whole function.
	callerContextLines = 5
	// maxCallerSnippets bounds what one finding quotes from its call sites.
	maxCallerSnippets = 10
	// maxIdentifiers bounds how many names one finding's text sends the search
	// after.
	maxIdentifiers = 8
)

// ExtractEvidence quotes the code a finding cites and the call sites of the
// identifiers it names, both bounded: the evidence is what a verifier reads
// instead of the finding's prose, so it has to be small enough to read.
func ExtractEvidence(fsys fs.FS, req EvidenceRequest) (EvidencePackage, error) {
	pkg := EvidencePackage{
		FindingTitle: req.Title,
		File:         req.File,
		LineStart:    req.LineStart,
		LineEnd:      req.LineEnd,
	}

	lines, err := readLines(fsys, req.File)
	if err != nil {
		return EvidencePackage{}, err
	}
	pkg.PrimaryCode = quoteLines(lines, req.LineStart, req.LineEnd, evidenceContextLines)

	identifiers := mentionedIdentifiers(req.Title + "\n" + req.Body + "\n" + req.Evidence)
	if len(identifiers) == 0 {
		return pkg, nil
	}
	paths, err := walkPaths(fsys, isQuotablePath)
	if err != nil {
		return EvidencePackage{}, err
	}
	for _, identifier := range identifiers {
		callers, err := callerSnippets(fsys, paths, identifier, req.File, maxCallerSnippets-len(pkg.CallerSnippets))
		if err != nil {
			return EvidencePackage{}, err
		}
		pkg.CallerSnippets = appendNew(pkg.CallerSnippets, callers...)
		if len(pkg.CallerSnippets) >= maxCallerSnippets {
			break
		}
	}
	return pkg, nil
}

// quoteLines quotes a range of a file and its context, numbered as the file has
// it. Nothing is quoted for a file that is not there, which is a fact about the
// finding rather than a failure of the review.
func quoteLines(lines []string, lineStart, lineEnd, context int) string {
	if len(lines) == 0 {
		return ""
	}
	start := max(1, lineStart-context)
	end := min(len(lines), max(lineStart, lineEnd)+context)
	if start > end {
		return ""
	}

	var builder strings.Builder
	for line := start; line <= end; line++ {
		builder.WriteString(strconv.Itoa(line))
		builder.WriteString(": ")
		builder.WriteString(lines[line-1])
		builder.WriteString("\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// callerSnippets quotes the call sites of one identifier: one snippet per
// matching line, in lexical file order, skipping the file the finding cites,
// because the finding's own line is not evidence of who calls it. budget bounds
// how many call sites are quoted in total.
func callerSnippets(fsys fs.FS, paths []string, identifier, excludeFile string, budget int) ([]string, error) {
	if budget <= 0 {
		return nil, nil
	}
	call := regexp.MustCompile(`\b` + regexp.QuoteMeta(identifier) + `\s*\(`)

	var snippets []string
	for _, file := range paths {
		if file == excludeFile {
			continue
		}
		lines, err := readLines(fsys, file)
		if err != nil {
			return nil, err
		}
		for index, line := range lines {
			if !call.MatchString(line) {
				continue
			}
			snippet := file + ":" + strconv.Itoa(index+1) + "\n" + quoteLines(lines, index+1, index+1, callerContextLines)
			snippets = append(snippets, snippet)
			if len(snippets) >= budget {
				return snippets, nil
			}
		}
	}
	return snippets, nil
}

// appendNew appends every value not already in values, keeping the order both
// sides were built in.
func appendNew(values []string, extra ...string) []string {
	for _, value := range extra {
		values = appendUnique(values, value)
	}
	return values
}

var (
	// backtickedRE reads the names a finding marked as code.
	backtickedRE = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*)`")
	// exportedRE reads the names a finding spelled the way an exported
	// identifier is spelled.
	exportedRE = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9]{2,})\b`)
	// calledRE reads the names a finding wrote as a call.
	calledRE = regexp.MustCompile(`\b([a-z_][a-z0-9_]{2,})\s*\(`)

	// identifierRE is the shape a name must have to be searched for as one.
	identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// commonIdentifierWords are the words findings use to talk about code rather
// than to name it. Searching the snapshot for them finds prose.
var commonIdentifierWords = map[string]bool{
	"the": true, "this": true, "that": true, "with": true, "from": true,
	"when": true, "where": true, "which": true, "there": true, "their": true,
	"returns": true, "return": true, "found": true, "check": true, "line": true,
	"file": true, "code": true, "issue": true, "error": true, "value": true,
	"values": true, "class": true, "function": true, "method": true,
	"should": true, "could": true, "would": true, "into": true, "over": true,
	"under": true, "each": true, "name": true, "data": true, "test": true,
	"tests": true,
}

// mentionedIdentifiers are the names a finding's text points at, in the order
// it mentions them, capped up front: each name costs a pass over the snapshot,
// and a body that names everything names nothing worth searching.
func mentionedIdentifiers(text string) []string {
	var identifiers []string
	for _, pattern := range []*regexp.Regexp{backtickedRE, exportedRE, calledRE} {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			name := strings.Trim(match[1], "` ")
			if len(name) < 3 || commonIdentifierWords[strings.ToLower(name)] || !identifierRE.MatchString(name) {
				continue
			}
			identifiers = appendUnique(identifiers, name)
		}
		if len(identifiers) >= maxIdentifiers {
			break
		}
	}
	if len(identifiers) > maxIdentifiers {
		identifiers = identifiers[:maxIdentifiers]
	}
	return identifiers
}
