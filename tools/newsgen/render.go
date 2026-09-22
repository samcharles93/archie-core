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

// redirectsJSONPath is the redirect table. The per-component pages were retired,
// so every `/news/<component>/<version>/` URL published before the merge is
// mapped to the unified page for that version. It is generated beside the pages
// for the same reason they are: a hand-kept list would drift from them.
const redirectsJSONPath = "docs/news/redirects.json"

// newsDir is where the MkDocs adapter writes the news index and its pages.
const newsDir = "docs/news"

// legacyComponentDirs are the URL namespaces the per-component pages used. Both
// are mapped for every version rather than only the component that released it,
// so a published URL resolves whether or not that component ever carried that
// version, and the table is derived from the version set instead of a list.
var legacyComponentDirs = []string{"archied", "archie"}

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
	redirects, err := encodeRedirects(releases)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		releasesJSONPath:      canonical,
		redirectsJSONPath:     redirects,
		newsDir + "/index.md": renderIndex(releases),
	}
	for _, r := range releases {
		files[fmt.Sprintf("%s/%s.md", newsDir, r.Version)] = renderRelease(r)
	}
	return files, nil
}

// encodeCanonical writes the JSON every host reads. HTML escaping is off so
// the artifact stays readable to a reviewer and to a non-Go consumer: the
// changelogs write `->` and `<memory>`, not `\u003e` and `\u003cmemory\u003e`.
func encodeCanonical(releases []release) ([]byte, error) {
	return encodeJSON(releasesJSONPath, releases)
}

// encodeRedirects writes the old-URL to new-URL table. encoding/json orders map
// keys, so the artifact is deterministic.
func encodeRedirects(releases []release) ([]byte, error) {
	table := map[string]string{}
	for _, r := range releases {
		for _, component := range legacyComponentDirs {
			table[fmt.Sprintf("/news/%s/%s/", component, r.Version)] = fmt.Sprintf("/news/%s/", r.Version)
		}
	}
	return encodeJSON(redirectsJSONPath, table)
}

func encodeJSON(path string, value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// renderIndex is the news page: one dated group per release day, newest first.
func renderIndex(releases []release) []byte {
	var b strings.Builder
	b.WriteString("# News\n\n")
	b.WriteString("Release notes, newest first.\n")
	lastDate := ""
	for _, r := range releases {
		if r.Date != lastDate {
			fmt.Fprintf(&b, "\n## %s\n\n", r.Date)
			lastDate = r.Date
		}
		fmt.Fprintf(&b, "- [%s](%s.md)\n", r.Version, r.Version)
	}
	return []byte(b.String())
}

// renderRelease is one version's page: the facts, and the changelog body
// verbatim, which carries that release's per-component sections.
func renderRelease(r release) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", r.Version)
	fmt.Fprintf(&b, "Released %s. [All news](index.md)\n\n", r.Date)
	b.WriteString(r.Body)
	b.WriteString("\n")
	return []byte(b.String())
}
