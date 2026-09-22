package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// changelogPath is the one file the release stream lives in. The two
// per-component changelogs were merged into it; their history is a section of
// this file rather than a separate source.
const changelogPath = "CHANGELOG.md"

// release is one changelog section: the facts a host needs to render a version.
// The per-component detail is inside Body, as labelled level-three sections.
type release struct {
	Version string `json:"version"`
	Date    string `json:"date"`
	Body    string `json:"body"`
}

// releaseHeading matches a changelog release heading. The date is optional in
// the pattern so `## [Unreleased]` is recognised and skipped rather than read as
// a release with an invented date.
var releaseHeading = regexp.MustCompile(`^## \[([^\]]+)\](?:\s*-\s*(\d{4}-\d{2}-\d{2}))?$`)

// inlineLink matches the target of a Markdown link or image.
var inlineLink = regexp.MustCompile(`\]\(([^)]+)\)`)

// linkScheme matches an absolute link target: a URL scheme, a site-root path or
// an in-page anchor. Everything else is relative to the changelog's directory.
var linkScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// parseChangelog reads the release sections out of the changelog. `Unreleased`
// is skipped, not dropped: it is the one place unfinished work is recorded and
// it carries no version to publish.
func parseChangelog(source string) ([]release, error) {
	var releases []release
	for _, section := range splitSections(source) {
		if !strings.HasPrefix(section.heading, "## [") {
			continue
		}
		match := releaseHeading.FindStringSubmatch(section.heading)
		if match == nil {
			return nil, fmt.Errorf("%s: %q is not a release heading; write `## [<version>] - <date>`", changelogPath, section.heading)
		}
		version, date := match[1], match[2]
		if version == "Unreleased" {
			continue
		}
		if date == "" {
			return nil, fmt.Errorf("%s: release %s has no date; write `## [<version>] - <date>`", changelogPath, version)
		}
		if section.body == "" {
			return nil, fmt.Errorf("%s: release %s has no notes", changelogPath, version)
		}
		if target, ok := relativeLink(section.body); ok {
			return nil, fmt.Errorf("%s: release %s has a relative link %q; release-notes links must be absolute", changelogPath, version, target)
		}
		releases = append(releases, release{
			Version: version,
			Date:    date,
			Body:    section.body,
		})
	}
	return releases, nil
}

// section is a level-two block: its heading line and the body that follows it.
type section struct {
	heading string
	body    string
}

// splitSections divides a changelog at every level-two heading. Level-three
// headings stay inside their section's body, and a `##` line inside a fenced
// code block is prose about markdown rather than a heading.
func splitSections(source string) []section {
	var sections []section
	current := section{}
	started := false
	inFence := false
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimRight(raw, " \t")
		if isFence(line) {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			if started {
				current.body = strings.Trim(current.body, "\n")
				sections = append(sections, current)
			}
			current = section{heading: line}
			started = true
			continue
		}
		if started {
			current.body += line + "\n"
		}
	}
	if started {
		current.body = strings.Trim(current.body, "\n")
		sections = append(sections, current)
	}
	return sections
}

// isFence reports whether a line opens or closes a fenced code block.
func isFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// relativeLink returns the first link target in the body that is not absolute.
func relativeLink(body string) (string, bool) {
	for _, match := range inlineLink.FindAllStringSubmatch(body, -1) {
		target := strings.TrimSpace(match[1])
		if i := strings.IndexAny(target, " \t"); i >= 0 {
			target = strings.TrimSpace(target[:i]) // drop an optional link title
		}
		target = strings.Trim(target, "<>")
		if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") {
			continue
		}
		if linkScheme.MatchString(target) {
			continue
		}
		return target, true
	}
	return "", false
}

// sortReleases orders releases newest first, then by descending version. Every
// host renders this order, so the canonical artifact is also the ordering
// contract.
func sortReleases(releases []release) {
	sort.SliceStable(releases, func(i, j int) bool {
		a, b := releases[i], releases[j]
		if a.Date != b.Date {
			return a.Date > b.Date
		}
		return compareVersions(a.Version, b.Version) > 0
	})
}

// compareVersions orders dotted versions by their numeric parts, so 1.9.10 is
// newer than 1.9.9. A version carrying a prerelease is older than the same
// version without one.
func compareVersions(a, b string) int {
	aCore, aPre := splitPrerelease(a)
	bCore, bPre := splitPrerelease(b)
	if c := compareDotted(aCore, bCore); c != 0 {
		return c
	}
	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	case aPre < bPre:
		return -1
	default:
		return 1
	}
}

func splitPrerelease(version string) (core, prerelease string) {
	if i := strings.IndexAny(version, "-+"); i >= 0 {
		return version[:i], version[i+1:]
	}
	return version, ""
}

func compareDotted(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv string
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		an, aerr := strconv.Atoi(av)
		bn, berr := strconv.Atoi(bv)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case av != bv:
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}
