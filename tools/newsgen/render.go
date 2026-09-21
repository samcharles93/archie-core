package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// releasesJSONPath is the canonical, host-neutral artifact every renderer
// reads. It lives with the news it describes, in the one directory this tool
// owns end-to-end, so it cannot collide with another generator's output.
const releasesJSONPath = "docs/news/releases.json"

// newsDir is where the MkDocs adapter writes the news index and its pages.
const newsDir = "docs/news"

// render returns every generated file keyed by repo-relative path. It is pure:
// the same releases always produce the same bytes, so the write path can skip a
// file that is already current.
func render(releases []release) (map[string][]byte, error) {
	if releases == nil {
		releases = []release{}
	}
	canonical, err := encodeCanonical(releases)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		releasesJSONPath:      canonical,
		newsDir + "/index.md": renderIndex(releases),
	}
	for _, r := range releases {
		files[fmt.Sprintf("%s/%s/%s.md", newsDir, r.Component, r.Version)] = renderRelease(r)
	}
	return files, nil
}

// encodeCanonical writes the JSON every host reads. HTML escaping is off so
// the artifact stays readable to a reviewer and to a non-Go consumer: the
// changelogs write `->` and `<memory>`, not `\u003e` and `\u003cmemory\u003e`.
func encodeCanonical(releases []release) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(releases); err != nil {
		return nil, fmt.Errorf("encode %s: %w", releasesJSONPath, err)
	}
	return buf.Bytes(), nil
}

// renderIndex is the news page: one dated group per release day, newest first.
func renderIndex(releases []release) []byte {
	var b strings.Builder
	b.WriteString("# News\n\n")
	b.WriteString("Release notes for archied and archie-agent, newest first.\n")
	lastDate := ""
	for _, r := range releases {
		if r.Date != lastDate {
			fmt.Fprintf(&b, "\n## %s\n\n", r.Date)
			lastDate = r.Date
		}
		fmt.Fprintf(&b, "- [%s %s](%s/%s.md)\n", r.Name, r.Version, r.Component, r.Version)
	}
	return []byte(b.String())
}

// renderRelease is one version's page: the facts, and the changelog body
// verbatim.
func renderRelease(r release) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s %s\n\n", r.Name, r.Version)
	fmt.Fprintf(&b, "Released %s. [All news](../index.md)\n\n", r.Date)
	b.WriteString(r.Body)
	b.WriteString("\n")
	return []byte(b.String())
}
