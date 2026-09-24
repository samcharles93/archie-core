package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// releasesJSONPath is the canonical, host-neutral artifact every renderer
// reads. It lives with the news it describes, in the one directory this tool
// owns end-to-end, so it cannot collide with another generator's output.
const releasesJSONPath = "docs/news/releases.json"

// redirectsJSONPath is the redirect table. The per-component pages were retired,
// so every `/news/<component>/<version>/` URL published before the merge is
// mapped to the unified page for that version. It is generated beside the
// canonical release facts so the two stay in sync.
const redirectsJSONPath = "docs/news/redirects.json"

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
		releasesJSONPath:  canonical,
		redirectsJSONPath: redirects,
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
