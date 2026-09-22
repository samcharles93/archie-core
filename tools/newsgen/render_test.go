package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sampleReleases() []release {
	return []release{
		{Version: "1.31.0", Date: "2026-09-22", Body: "### archied — Gateway\n\n- a bullet\n\n### archie-agent\n\n- a runtime bullet"},
		{Version: "1.30.0", Date: "2026-09-20", Body: "Prose."},
	}
}

func TestRenderEmitsTheCanonicalJSON(t *testing.T) {
	files, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	body, ok := files[releasesJSONPath]
	if !ok {
		t.Fatalf("render produced no %s (files: %v)", releasesJSONPath, keys(files))
	}
	var decoded []release
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("canonical JSON does not decode into []release: %v", err)
	}
	if !reflect.DeepEqual(decoded, sampleReleases()) {
		t.Fatalf("round trip = %+v, want %+v", decoded, sampleReleases())
	}
}

func TestRenderJSONKeepsHTMLCharactersReadable(t *testing.T) {
	releases := []release{{
		Version: "1.0.0", Date: "2026-01-01",
		Body: "a -> b and <memory>",
	}}
	files, err := render(releases)
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	got := string(files[releasesJSONPath])
	for _, want := range []string{"a -> b", "<memory>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("canonical JSON escaped %q:\n%s", want, got)
		}
	}
}

func TestRenderWritesOnePagePerVersion(t *testing.T) {
	files, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	for _, path := range []string{
		"docs/news/index.md",
		"docs/news/1.31.0.md",
		"docs/news/1.30.0.md",
		"docs/news/releases.json",
		"docs/news/redirects.json",
	} {
		if _, ok := files[path]; !ok {
			t.Fatalf("render did not produce %s (files: %v)", path, keys(files))
		}
	}
	if len(files) != 5 {
		t.Fatalf("render produced %d files, want 5: %v", len(files), keys(files))
	}
}

func TestRenderRedirectsEveryRetiredComponentURL(t *testing.T) {
	files, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	var table map[string]string
	if err := json.Unmarshal(files[redirectsJSONPath], &table); err != nil {
		t.Fatalf("redirect table does not decode: %v", err)
	}
	want := map[string]string{
		"/news/archied/1.31.0/": "/news/1.31.0/",
		"/news/archie/1.31.0/":  "/news/1.31.0/",
		"/news/archied/1.30.0/": "/news/1.30.0/",
		"/news/archie/1.30.0/":  "/news/1.30.0/",
	}
	if !reflect.DeepEqual(table, want) {
		t.Fatalf("redirect table = %v, want %v", table, want)
	}
}

func TestRenderIndexGroupsByDate(t *testing.T) {
	files, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	want := `# News

Release notes, newest first.

## 2026-09-22

- [1.31.0](1.31.0.md)

## 2026-09-20

- [1.30.0](1.30.0.md)
`
	if got := string(files["docs/news/index.md"]); got != want {
		t.Fatalf("index markdown =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderReleasePageKeepsTheBodyVerbatim(t *testing.T) {
	files, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	want := `# 1.31.0

Released 2026-09-22. [All news](index.md)

### archied — Gateway

- a bullet

### archie-agent

- a runtime bullet
`
	if got := string(files["docs/news/1.31.0.md"]); got != want {
		t.Fatalf("release page =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("first render error = %v", err)
	}
	second, err := render(sampleReleases())
	if err != nil {
		t.Fatalf("second render error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two renders of the same releases differ")
	}
}

func TestRenderHandlesNoReleases(t *testing.T) {
	files, err := render(nil)
	if err != nil {
		t.Fatalf("render error = %v", err)
	}
	if got := string(files[releasesJSONPath]); got != "[]\n" {
		t.Fatalf("empty canonical JSON = %q, want %q", got, "[]\n")
	}
	if got := string(files[redirectsJSONPath]); got != "{}\n" {
		t.Fatalf("empty redirect table = %q, want %q", got, "{}\n")
	}
	if got := string(files["docs/news/index.md"]); got != "# News\n\nRelease notes, newest first.\n" {
		t.Fatalf("empty index = %q", got)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
