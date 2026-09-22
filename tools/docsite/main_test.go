package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishedSetIsTheDecision pins the count against the real tree. The number
// is the decision this tool carries: everything under docs/ is public except five
// named exclusions, so a page added or removed on purpose has to be
// acknowledged here rather than sliding into the published site.
func TestPublishedSetIsTheDecision(t *testing.T) {
	doc, err := load("../..")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(doc.Pages) != 23 {
		titles := make([]string, 0, len(doc.Pages))
		for _, entry := range doc.Pages {
			titles = append(titles, entry.URL)
		}
		t.Fatalf("pages = %d, want the 21 the published set declares:\n  %s", len(doc.Pages), strings.Join(titles, "\n  "))
	}

	published := map[string]bool{}
	for _, entry := range doc.Pages {
		published[entry.URL] = true
		if entry.Body == "" {
			t.Errorf("%s carries no body; the artifact is the source a renderer needs", entry.URL)
		}
		if !strings.HasPrefix(entry.Body, "#") {
			t.Errorf("%s body does not start with a heading: %q", entry.URL, firstLine(entry.Body))
		}
	}
	for _, held := range []string{"/docs/archive/", "/docs/inspiration/", "/docs/news/", "/docs/github-token/"} {
		for url := range published {
			if strings.HasPrefix(url, held) {
				t.Errorf("%s is published; it is held back deliberately", url)
			}
		}
	}
	for _, want := range []string{
		"/docs/",
		"/docs/architecture/organisation/",
		"/docs/prds/ui-service-boundary/",
		"/docs/prds/service-decomposition/",
		"/docs/prds/config-schema/",
		// Cited by documents of record, so a blanket prds/* exclusion had left
		// these links 404 on the published site.
		"/docs/prds/memory-engine-unification/",
		"/docs/prds/messaging-service-boundary/",
	} {
		if !published[want] {
			t.Errorf("%s is not published; the set declares it", want)
		}
	}
	// An unpublished PRD is the common case, so one absence is asserted to keep the
	// rule from being satisfied by publishing everything.
	if published["/docs/prds/curator-sampler-wave1/"] {
		t.Error("an open PRD is published; prds/ is held back except the ratified and cited ones")
	}
}

func firstLine(body string) string {
	line := body
	if index := strings.IndexByte(body, '\n'); index >= 0 {
		line = body[:index]
	}
	return line
}

// TestPageURLsAreTheStaticSitePaths: every existing link points at the
// directory-style path a static site produces, so the artifact has to reproduce
// it exactly rather than inventing a cleaner one.
func TestPageURLsAreTheStaticSitePaths(t *testing.T) {
	for _, tc := range []struct{ relative, want string }{
		{"index.md", "/docs/"},
		{"architecture/index.md", "/docs/architecture/"},
		{"architecture/organisation.md", "/docs/architecture/organisation/"},
		{"prds/config-schema.md", "/docs/prds/config-schema/"},
		{"guides/nested/deep.md", "/docs/guides/nested/deep/"},
	} {
		if got := pageURL(tc.relative); got != tc.want {
			t.Errorf("pageURL(%q) = %q, want %q", tc.relative, got, tc.want)
		}
	}
}

// TestNavMirrorsTheLayout: the tree is derived from the directory structure, so a
// page added under a directory appears in the sidebar without a second edit -- the
// property MkDocs had, and the one a hand-maintained nav loses.
func TestNavMirrorsTheLayout(t *testing.T) {
	nav := navFor([]page{
		{URL: "/docs/", Title: "Archie"},
		{URL: "/docs/architecture/", Title: "Architecture"},
		{URL: "/docs/architecture/organisation/", Title: "Organisation"},
		{URL: "/docs/architecture/policy/", Title: "Policy"},
		{URL: "/docs/guides/first-playbook/", Title: "Building your first playbook binding"},
	})
	if len(nav) != 3 {
		t.Fatalf("nav = %+v, want home plus two groups", nav)
	}
	if nav[0].URL != "/docs/" {
		t.Errorf("first entry = %+v, want the home page", nav[0])
	}
	architecture := nav[1]
	if architecture.Title != "Architecture" || architecture.URL != "/docs/architecture/" {
		t.Errorf("group = %+v, want the section with its own landing page", architecture)
	}
	if len(architecture.Items) != 2 || architecture.Items[0].Title != "Organisation" {
		t.Errorf("items = %+v, want the section's pages in order", architecture.Items)
	}
	// A directory holding one page is still a group: collapsing it would be a
	// shape this artifact invented rather than one it reports.
	guides := nav[2]
	if guides.Title != "Guides" || len(guides.Items) != 1 || guides.Items[0].URL != "/docs/guides/first-playbook/" {
		t.Errorf("guides = %+v, want a group wrapping its single page", guides)
	}
}

// TestCheckFailsOnAStaleArtifactAndLeavesTheTreeAlone: check is what runs in the
// gate, so it has to fail on a source edit that never reached the artifact without
// writing anything itself.
func TestCheckFailsOnAStaleArtifactAndLeavesTheTreeAlone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "index.md"), "# Archie\n\nHello.\n")
	writeFile(t, filepath.Join(root, "docs", "architecture", "policy.md"), "# Policy\n\nRules.\n")
	writeFile(t, filepath.Join(root, "docs", "archive", "old.md"), "# Old\n\nSuperseded.\n")
	stale := filepath.Join(root, "docs", "data", "generated", "docs.json")
	writeFile(t, stale, "{}\n")

	if err := check(root); err == nil {
		t.Fatal("check(stale) = nil, want a failure: the committed artifact disagrees with the sources")
	}
	before, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != "{}\n" {
		t.Fatalf("check rewrote the artifact: %q", before)
	}

	if err := write(root); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := check(root); err != nil {
		t.Fatalf("check after write = %v, want it to pass", err)
	}

	doc, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Pages) != 2 {
		t.Fatalf("pages = %d, want the two published pages (archive/ is held back)", len(doc.Pages))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLinksResolveInsideTheSetAndFailOutside pins both halves of link handling.
// An intra-docs link becomes the target page's url, using the same rule that
// builds the target's url, and anchors and scheme-qualified links pass through. A
// link whose target the set holds back fails the load, naming the file, the line
// and the target: that refusal is what made a blanket prds/* exclusion visible,
// because three citations from settled architecture had been 404s on the published
// site, and a hopeful rewrite would have hidden them.
func TestLinksResolveInsideTheSetAndFailOutside(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "organisation.md"), "# Organisation\n\nBack [home](index.md).\n")
	writeFile(t, filepath.Join(root, "docs", "archive", "old.md"), "# Old\n")
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\nSee [Organisation](organisation.md#organisation).\n\n[Old](archive/old.md) is held back.\n")

	_, err := load(root)
	if err == nil {
		t.Fatal("load = nil, want a refusal: a published page links into archive/, which the set holds back")
	}
	for _, want := range []string{"index.md:5", "archive/old.md", "not in the published set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	// The same tree without the out-of-set link: the in-set link becomes the target
	// page's url, and the anchor rides along.
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\nSee [Organisation](organisation.md#organisation), [OpenAI](https://openai.com/) and [below](#anchors).\n")
	doc, err := load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var index string
	for _, entry := range doc.Pages {
		if entry.URL == "/docs/" {
			index = entry.Body
		}
	}
	for _, want := range []string{
		"(/docs/organisation/#organisation)",
		"(https://openai.com/)",
		"(#anchors)",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("resolved body = %q, want it to contain %q", index, want)
		}
	}
}

// TestFragmentsAreValidatedAgainstTheTargetsHeadings: a link can resolve to a page
// and still be broken, because a fragment that matches no heading lands the reader
// at the top of a long document with no indication why. The slug rule mirrors a
// Markdown renderer's table of contents, so the ids it accepts are the ids the
// rendered page carries.
func TestFragmentsAreValidatedAgainstTheTargetsHeadings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "decisions.md"),
		"# Decisions\n\n## 5. Memory placement and storage\n\n## Bindings & secrets\n")
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\n[Good](decisions.md#5-memory-placement-and-storage).\n\n[Bad](decisions.md#5).\n")

	_, err := load(root)
	if err == nil {
		t.Fatal("load = nil, want a refusal: #5 matches no heading on the target page")
	}
	for _, want := range []string{"index.md:5", "#5", "no such heading"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "#5-memory-placement-and-storage") {
		t.Errorf("error = %q, want only the broken fragment named", err)
	}

	// A fragment that does match, including one whose heading has punctuation and an
	// ampersand, passes and is carried verbatim.
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\n[Good](decisions.md#5-memory-placement-and-storage).\n\n[Also](decisions.md#bindings-secrets).\n")
	doc, err := load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, entry := range doc.Pages {
		if entry.URL != "/docs/" {
			continue
		}
		if !strings.Contains(entry.Body, "(/docs/decisions/#bindings-secrets)") {
			t.Errorf("resolved body = %q, want the ampersand heading's slug", entry.Body)
		}
	}
}
