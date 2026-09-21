package main

import (
	"strings"
	"testing"
)

const sampleChangelog = `# archied changelog

## [1.31.0] - 2026-09-22

### A heading

- one bullet

## [1.30.0] - 2026-09-20

Just prose.

## [Unreleased]

Future work.

## Licence

Not a release section.
`

func TestParseChangelogReadsReleaseSections(t *testing.T) {
	got, err := parseChangelog(components[0], sampleChangelog)
	if err != nil {
		t.Fatalf("parseChangelog error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d releases, want 2: %+v", len(got), got)
	}
	first := got[0]
	if first.Component != "archied" || first.Name != "archied" {
		t.Fatalf("first release component/name = %q/%q, want archied/archied", first.Component, first.Name)
	}
	if first.Version != "1.31.0" || first.Date != "2026-09-22" {
		t.Fatalf("first release version/date = %q/%q", first.Version, first.Date)
	}
	if first.Body != "### A heading\n\n- one bullet" {
		t.Fatalf("first release body = %q", first.Body)
	}
	if got[1].Version != "1.30.0" || got[1].Body != "Just prose." {
		t.Fatalf("second release = %+v", got[1])
	}
}

func TestParseChangelogSkipsUnreleased(t *testing.T) {
	got, err := parseChangelog(components[0], sampleChangelog)
	if err != nil {
		t.Fatalf("parseChangelog error = %v", err)
	}
	for _, r := range got {
		if r.Version == "Unreleased" {
			t.Fatal("Unreleased was published as a release")
		}
	}
}

func TestParseChangelogRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantSub string
	}{
		{
			name:    "release heading without a date",
			source:  "## [1.2.0]\n\nNotes.\n",
			wantSub: "no date",
		},
		{
			name:    "release heading with an invalid date",
			source:  "## [1.2.0] - 2026-9-2\n\nNotes.\n",
			wantSub: "not a release heading",
		},
		{
			name:    "release without notes",
			source:  "## [1.2.0] - 2026-01-01\n",
			wantSub: "no notes",
		},
		{
			name:    "relative markdown link",
			source:  "## [1.2.0] - 2026-01-01\n\nSee [the guide](guides/first-playbook.md).\n",
			wantSub: "relative link",
		},
		{
			name:    "relative image link",
			source:  "## [1.2.0] - 2026-01-01\n\n![shot](images/shot.png)\n",
			wantSub: "relative link",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseChangelog(components[0], tt.source)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantSub)
			}
		})
	}
}

func TestParseChangelogAllowsAbsoluteLinks(t *testing.T) {
	source := `## [1.2.0] - 2026-01-01

- [vulnerability](https://pkg.go.dev/vuln/GO-2026-6443), [anchor](#section),
  [root](/absolute), [angle](<https://example.com>), [mail](mailto:a@b.c).
`
	if _, err := parseChangelog(components[0], source); err != nil {
		t.Fatalf("parseChangelog error = %v", err)
	}
}

func TestParseChangelogKeepsFencedHeadingsInTheBody(t *testing.T) {
	source := "## [1.2.0] - 2026-01-01\n\n```sh\n## not a heading\n```\n\n- after the fence\n"
	got, err := parseChangelog(components[0], source)
	if err != nil {
		t.Fatalf("parseChangelog error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d releases, want 1", len(got))
	}
	want := "```sh\n## not a heading\n```\n\n- after the fence"
	if got[0].Body != want {
		t.Fatalf("body = %q, want %q", got[0].Body, want)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.9.10", "1.9.9", 1},
		{"1.10.0", "1.9.0", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.0.0", "2.0.0", -1},
		{"1.9.0", "1.9.0", 0},
		{"1.2.3", "1.2.3-rc1", 1},
	}
	for _, tt := range tests {
		if got := compareVersions(tt.a, tt.b); got != tt.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSortReleasesNewestFirstThenComponentThenVersion(t *testing.T) {
	releases := []release{
		{Component: "archie", Name: "archie-agent", Version: "1.31.0", Date: "2026-09-22"},
		{Component: "archied", Name: "archied", Version: "1.9.9", Date: "2026-09-22"},
		{Component: "archied", Name: "archied", Version: "1.30.0", Date: "2026-09-20"},
		{Component: "archied", Name: "archied", Version: "1.9.10", Date: "2026-09-22"},
		{Component: "archied", Name: "archied", Version: "1.31.0", Date: "2026-09-22"},
	}
	sortReleases(releases)
	want := []string{"archied/1.31.0", "archied/1.9.10", "archied/1.9.9", "archie/1.31.0", "archied/1.30.0"}
	for i, w := range want {
		got := releases[i].Component + "/" + releases[i].Version
		if got != w {
			t.Fatalf("position %d = %s, want %s (all: %+v)", i, got, w, releases)
		}
	}
}
