package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// documentFor is the generated artifact for one set.
func documentFor(t *testing.T, generated []setDocument, set docSet) document {
	t.Helper()
	for _, entry := range generated {
		if entry.Set.Name == set.Name {
			return entry.Document
		}
	}
	t.Fatalf("no %s set was generated", set.Name)
	return document{}
}

// TestTheSplitIsByAudience pins the two sets against the real tree. The public
// set is a decision page by page, so its contents are listed: a page joining or
// leaving what a customer reads has to be acknowledged here rather than sliding
// onto the site. The development set is a rule rather than a list, because a PRD
// is added most weeks and pinning a count would make this test a chore that
// teaches nothing.
func TestTheSplitIsByAudience(t *testing.T) {
	generated, err := load("../..")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	public := documentFor(t, generated, publicDocs)
	development := documentFor(t, generated, developmentDocs)

	publicURLs := urlSet(t, public)
	if len(publicURLs) != 2 {
		t.Errorf("public set = %v, want the landing page and the guide", sortedKeys(publicURLs))
	}
	for _, want := range []string{"/docs/", "/docs/guides/first-playbook/"} {
		if !publicURLs[want] {
			t.Errorf("%s is not in the public set", want)
		}
	}

	developmentURLs := urlSet(t, development)
	for _, want := range []string{
		"/dev/architecture/",
		"/dev/architecture/organisation/",
		"/dev/prds/ui-service-boundary/",
		// Selective PRD publication is gone: an open PRD is a development page like
		// any other, which is what removes the pressure that published five of them
		// to keep citations from settled architecture alive.
		"/dev/prds/curator-sampler-wave1/",
	} {
		if !developmentURLs[want] {
			t.Errorf("%s is not in the development set", want)
		}
	}

	// Neither set may carry the other's pages, and neither may carry a held-back
	// tree.
	for url := range publicURLs {
		if strings.HasPrefix(url, "/docs/architecture/") || strings.HasPrefix(url, "/docs/prds/") {
			t.Errorf("%s is in the public set; architecture/ and prds/ are development pages", url)
		}
	}
	for url := range developmentURLs {
		if !strings.HasPrefix(url, "/dev/") {
			t.Errorf("%s is in the development set but is not served under its base", url)
		}
	}
	for _, held := range []string{"archive/", "inspiration/", "news/", "github-token/"} {
		for _, url := range append(sortedKeys(publicURLs), sortedKeys(developmentURLs)...) {
			if strings.Contains(url, held) {
				t.Errorf("%s is published; it is held back deliberately", url)
			}
		}
	}
}

// urlSet indexes a set's pages by url, asserting on the way that each carries the
// body a renderer needs.
func urlSet(t *testing.T, doc document) map[string]bool {
	t.Helper()
	urls := map[string]bool{}
	for _, entry := range doc.Pages {
		urls[entry.URL] = true
		if entry.Body == "" {
			t.Errorf("%s carries no body; the artifact is the source a renderer needs", entry.URL)
		}
		if !strings.HasPrefix(entry.Body, "#") {
			t.Errorf("%s body does not start with a heading: %q", entry.URL, firstLine(entry.Body))
		}
	}
	return urls
}

func sortedKeys(urls map[string]bool) []string {
	keys := make([]string, 0, len(urls))
	for url := range urls {
		keys = append(keys, url)
	}
	return keys
}

func firstLine(body string) string {
	line, _, _ := strings.Cut(body, "\n")
	return line
}

// TestPageURLsAreTheStaticSitePaths: every existing link points at the
// directory-style path a static site produces, so the artifact has to reproduce
// it exactly rather than inventing a cleaner one. The base is the one its set is
// served under.
func TestPageURLsAreTheStaticSitePaths(t *testing.T) {
	for _, tc := range []struct{ relative, want string }{
		{"index.md", "/docs/"},
		{"guides/first-playbook.md", "/docs/guides/first-playbook/"},
		{"guides/nested/deep.md", "/docs/guides/nested/deep/"},
		{"architecture/index.md", "/dev/architecture/"},
		{"architecture/organisation.md", "/dev/architecture/organisation/"},
		{"prds/config-schema.md", "/dev/prds/config-schema/"},
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
		{URL: "/docs/guides/first-playbook/", Title: "Building your first playbook binding"},
	}, publicDocs.URLPrefix)
	if len(nav) != 2 {
		t.Fatalf("nav = %+v, want home plus one group", nav)
	}
	if nav[0].URL != "/docs/" {
		t.Errorf("first entry = %+v, want the home page", nav[0])
	}
	// A directory holding one page is still a group: collapsing it would be a
	// shape this artifact invented rather than one it reports.
	guides := nav[1]
	if guides.Title != "Guides" || len(guides.Items) != 1 || guides.Items[0].URL != "/docs/guides/first-playbook/" {
		t.Errorf("guides = %+v, want a group wrapping its single page", guides)
	}

	// The development set has no page of its own at its base, so its sidebar opens
	// with the sections rather than a home link.
	devNav := navFor([]page{
		{URL: "/dev/architecture/", Title: "Architecture"},
		{URL: "/dev/architecture/organisation/", Title: "Organisation"},
		{URL: "/dev/architecture/policy/", Title: "Policy"},
		{URL: "/dev/prds/config-schema/", Title: "Config schema"},
	}, developmentDocs.URLPrefix)
	if len(devNav) != 2 {
		t.Fatalf("nav = %+v, want the two sections and no home entry", devNav)
	}
	architecture := devNav[0]
	if architecture.Title != "Architecture" || architecture.URL != "/dev/architecture/" {
		t.Errorf("group = %+v, want the section with its own landing page", architecture)
	}
	if len(architecture.Items) != 2 || architecture.Items[0].Title != "Organisation" {
		t.Errorf("items = %+v, want the section's pages in order", architecture.Items)
	}
}

// TestCheckFailsOnAStaleArtifactAndLeavesTheTreeAlone: check is what runs in the
// gate, so it has to fail on a source edit that never reached an artifact without
// writing anything itself. A stale development artifact fails it exactly as a
// stale public one does; before both were checked, a change to a development page
// could land with no artifact carrying it.
func TestCheckFailsOnAStaleArtifactAndLeavesTheTreeAlone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "index.md"), "# Archie\n\nHello.\n")
	writeFile(t, filepath.Join(root, "docs", "architecture", "policy.md"), "# Policy\n\nRules.\n")
	writeFile(t, filepath.Join(root, "docs", "archive", "old.md"), "# Old\n\nSuperseded.\n")

	if err := check(root); err == nil {
		t.Fatal("check(missing) = nil, want a failure: no artifact is committed")
	}

	if err := write(root); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := check(root); err != nil {
		t.Fatalf("check after write = %v, want it to pass", err)
	}

	stale := filepath.Join(root, "docs", "data", "generated", "dev-docs.json")
	writeFile(t, stale, "{}\n")
	if err := check(root); err == nil {
		t.Fatal("check(stale development artifact) = nil, want a failure")
	}
	before, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != "{}\n" {
		t.Fatalf("check rewrote the artifact: %q", before)
	}

	generated, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	if pages := documentFor(t, generated, publicDocs).Pages; len(pages) != 1 {
		t.Fatalf("public pages = %d, want the landing page alone", len(pages))
	}
	if pages := documentFor(t, generated, developmentDocs).Pages; len(pages) != 1 {
		t.Fatalf("development pages = %d, want the architecture page alone (archive/ is held back)", len(pages))
	}
}

// TestUntrackedPagesDoNotReachTheArtifact pins why the artifact is derived from
// the commit rather than from the working tree. docs:artifact:check runs in the
// gate and reads the sources from disk, so an untracked page under docs/ made
// the committed artifact look stale. The gate then demanded a regeneration that
// was already correct, and that regeneration would have committed a page no
// commit carries. It cost two sessions in one day: a migration branch, and an
// unrelated writer adding a PRD.
func TestUntrackedPagesDoNotReachTheArtifact(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "index.md"), "# Archie\n\nHello.\n")
	writeFile(t, filepath.Join(root, "docs", "architecture", "policy.md"), "# Policy\n\nRules.\n")
	gitIn(t, root, "init", "--quiet")
	gitIn(t, root, "add", "docs/index.md", "docs/architecture/policy.md")

	if err := write(root); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Untracked: no commit carries it, so the committed artifact is not required
	// to contain it and check must not claim the artifact is stale.
	writeFile(t, filepath.Join(root, "docs", "prds", "scratch.md"), "# Scratch\n\nNot committed.\n")
	if err := check(root); err != nil {
		t.Fatalf("check with an untracked page = %v, want it to pass", err)
	}

	// Staged: this commit is about to carry it, so the artifact must too.
	gitIn(t, root, "add", "docs/prds/scratch.md")
	if err := check(root); err == nil {
		t.Fatal("check with a staged page = nil, want a failure: the artifact does not carry it")
	}
	if err := write(root); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := check(root); err != nil {
		t.Fatalf("check after regenerating for the staged page = %v, want it to pass", err)
	}
}

// gitIn runs one git command in dir, failing loudly rather than skipping: task
// check already requires git for proto:lint, so a test that quietly passed
// without it would be asserting nothing.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=docsite", "GIT_AUTHOR_EMAIL=docsite@example.invalid",
		"GIT_COMMITTER_NAME=docsite", "GIT_COMMITTER_EMAIL=docsite@example.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
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

// TestLinksResolveAcrossSetsAndFailOutsideThem pins both halves of link handling.
// An intra-docs link becomes the target page's url, using the same rule that
// builds the target's url, so a citation that crosses from one set to the other
// lands on the base its target is actually served under. Anchors and
// scheme-qualified links pass through. A link whose target neither set carries
// fails the load, naming the file, the line and the target, because a hopeful
// rewrite would hide a 404 rather than report it.
func TestLinksResolveAcrossSetsAndFailOutsideThem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "architecture", "organisation.md"),
		"# Organisation\n\nBack [home](../index.md).\n")
	writeFile(t, filepath.Join(root, "docs", "archive", "old.md"), "# Old\n")
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\nSee [Organisation](architecture/organisation.md#organisation).\n\n[Old](archive/old.md) is held back.\n")

	_, err := load(root)
	if err == nil {
		t.Fatal("load = nil, want a refusal: a page links into archive/, which is held back")
	}
	for _, want := range []string{"index.md:5", "archive/old.md", "not in the published set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	// The same tree without the out-of-set link: each link becomes the target page's
	// url, under the target's own base.
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\nSee [Organisation](architecture/organisation.md#organisation), [OpenAI](https://openai.com/) and [below](#anchors).\n")
	generated, err := load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	index := bodyOf(t, documentFor(t, generated, publicDocs), "/docs/")
	for _, want := range []string{
		"(/dev/architecture/organisation/#organisation)",
		"(https://openai.com/)",
		"(#anchors)",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("public body = %q, want it to contain %q", index, want)
		}
	}
	organisation := bodyOf(t, documentFor(t, generated, developmentDocs), "/dev/architecture/organisation/")
	if !strings.Contains(organisation, "(/docs/)") {
		t.Errorf("development body = %q, want the link back to the public landing page", organisation)
	}
}

func bodyOf(t *testing.T, doc document, url string) string {
	t.Helper()
	for _, entry := range doc.Pages {
		if entry.URL == url {
			return entry.Body
		}
	}
	t.Fatalf("no page at %s", url)
	return ""
}

// TestFragmentsAreValidatedAgainstTheTargetsHeadings: a link can resolve to a page
// and still be broken, because a fragment that matches no heading lands the reader
// at the top of a long document with no indication why. The slug rule mirrors a
// Markdown renderer's table of contents, so the ids it accepts are the ids the
// rendered page carries.
func TestFragmentsAreValidatedAgainstTheTargetsHeadings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "architecture", "decisions.md"),
		"# Decisions\n\n## 5. Memory placement and storage\n\n## Bindings & secrets\n")
	writeFile(t, filepath.Join(root, "docs", "index.md"),
		"# Archie\n\n[Good](architecture/decisions.md#5-memory-placement-and-storage).\n\n[Bad](architecture/decisions.md#5).\n")

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
		"# Archie\n\n[Good](architecture/decisions.md#5-memory-placement-and-storage).\n\n[Also](architecture/decisions.md#bindings-secrets).\n")
	generated, err := load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if index := bodyOf(t, documentFor(t, generated, publicDocs), "/docs/"); !strings.Contains(index, "(/dev/architecture/decisions/#bindings-secrets)") {
		t.Errorf("resolved body = %q, want the ampersand heading's slug", index)
	}
}
