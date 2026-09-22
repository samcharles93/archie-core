// Command docsite derives the public documentation set into one canonical,
// host-neutral artifact.
//
// The Markdown is the source; docs/data/generated/docs.json is the fact set every
// renderer reads. MkDocs was the first renderer and the Astro app is the second,
// so the artifact is the product and the Markdown stays the source --
// tools/newsgen states the same principle for releases, where the JSON is
// canonical and the Markdown is an adapter.
//
// The published set is declared here rather than in a site configuration, because
// the exclusions are decisions rather than details: see publishRules. A renderer
// that reads this artifact needs no copy of them.
//
// Output is generated and committed:
//
//	docs/data/generated/docs.json
//
// `docsite check` regenerates the artifact in a temporary directory and fails when
// the committed one differs, without touching the tree.
//
// This tool keeps no registry of the generated directory it writes into: that
// directory is accounted for by docsgen's generatedArtifacts, which names this
// file, so a second registry here would be a second home for one fact.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// artifactPath is the committed artifact this tool owns, relative to the
// repository root.
const artifactPath = "docs/data/generated/docs.json"

// docsDir is the Markdown source tree, relative to the repository root.
const docsDir = "docs"

// urlPrefix is the path every page is served under. The directory-style paths
// MkDocs produced are kept exactly, so existing links keep working.
const urlPrefix = "/docs/"

type mode int

const (
	modeWrite mode = iota
	modeCheck
)

// options is a parsed command line.
type options struct {
	mode     mode
	repoRoot string
}

func main() {
	parsed, err := parseOptions(os.Args[1:])
	if err != nil {
		log.Fatalf("docsite: %v", err)
	}
	if parsed.mode == modeCheck {
		err = check(parsed.repoRoot)
	} else {
		err = write(parsed.repoRoot)
	}
	if err != nil {
		log.Fatalf("docsite: %v", err)
	}
}

// parseOptions resolves the optional `check` subcommand and the flags that may
// follow it. The subcommand is peeled off before flag parsing rather than left for
// it to ignore: `docsite --repo-root .. check` would otherwise fall through to the
// write path and rewrite the artifact check promises not to touch.
func parseOptions(args []string) (options, error) {
	parsed := options{repoRoot: "."}
	if len(args) > 0 && args[0] == "check" {
		parsed.mode = modeCheck
		args = args[1:]
	}
	flags := flag.NewFlagSet("docsite", flag.ContinueOnError)
	flags.StringVar(&parsed.repoRoot, "repo-root", ".", "repository root to read docs from")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return parsed, nil
}

// document is the artifact. Its keys are a frozen contract: a renderer is built
// against them, so a rename here breaks a consumer this repository does not
// compile.
type document struct {
	Pages []page    `json:"pages"`
	Nav   []navNode `json:"nav"`
}

// page is one published document. Body is the raw Markdown source: presentation
// belongs to the renderer, so this artifact deliberately does not carry HTML.
type page struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Section string `json:"section"`
	Body    string `json:"body"`
}

// navNode is one entry in the sidebar: a link when URL is set, a group when Items
// is.
type navNode struct {
	Title string    `json:"title"`
	URL   string    `json:"url,omitempty"`
	Items []navNode `json:"items,omitempty"`
}

// publishRules is the published set, and the reasons, carried from the site
// configuration this replaces. Everything under docs/ is public except:
//
//   - archive/     superseded design. Publishing it competes with the current
//     architecture for a reader's attention, which is the exact failure this
//     repository already fights.
//   - inspiration/ research notes that discuss other products by name.
//   - prds/        open design decisions rather than settled architecture. Two
//     kinds of PRD are published anyway, and for different reasons:
//     · ui-service-boundary.md, service-decomposition.md and config-schema.md
//     are ratified and read as reference.
//     · memory-engine-unification.md and messaging-service-boundary.md are
//     cited by documents of record -- settled architecture and the migration
//     register -- so excluding them withheld nothing a reader was spared: it
//     broke citations. A blanket prds/* rule had left those links 404 on the
//     published site, which is worse than either publishing or not linking.
//   - news/        release notes, which tools/newsgen owns and a renderer reads
//     from releases.json.
//   - github-token.md  setup notes about credentials.
//
// Widening the set means editing this list. The comment is the record: a renderer
// that reads the artifact cannot see why a page is absent, and neither can the
// next person deciding whether a citation should publish its target.
var (
	heldBackTrees = map[string]bool{"archive": true, "inspiration": true, "news": true}
	publishedPRDs = map[string]bool{
		"ui-service-boundary.md":        true,
		"service-decomposition.md":      true,
		"config-schema.md":              true,
		"memory-engine-unification.md":  true,
		"messaging-service-boundary.md": true,
	}
	heldBackPages = map[string]bool{"github-token.md": true}
)

// heldBack reports whether one path relative to docs/ is deliberately absent from
// the published set. Links and pages are judged by this one rule, so a page is
// never published while a link to it is refused, or the reverse.
func heldBack(relative string) bool {
	if heldBackPages[relative] {
		return true
	}
	segments := strings.Split(relative, "/")
	if heldBackTrees[segments[0]] {
		return true
	}
	if segments[0] == "prds" && len(segments) > 1 && !publishedPRDs[strings.Join(segments[1:], "/")] {
		return true
	}
	return false
}

// published reports whether one path relative to docs/ is a page the artifact
// carries.
func published(relative string) bool {
	return strings.HasSuffix(relative, ".md") && !heldBack(relative)
}

// intraDocsLink matches a Markdown inline link or image. The target is the only
// part this tool rewrites; everything else in the match is preserved.
var intraDocsLink = regexp.MustCompile(`\]\(\s*([^)\s]+)`)

// unresolvedLink names a link the artifact cannot honestly carry: its target is a
// page or asset the published set holds back. It is reported rather than rewritten
// to a hopeful URL or dropped, because such a link exists only because a site build
// used to publish the whole tree -- it is exactly what the exclusions break, and
// the reader deserves the list rather than a guess.
type unresolvedLink struct {
	File   string
	Line   int
	Target string
	// Why names what is wrong, because the two cases need different repairs: a
	// target the set holds back is published or unlinked, and a fragment that
	// matches no heading is a broken citation to fix in the prose.
	Why string
}

func (u unresolvedLink) Error() string {
	return fmt.Sprintf("%s:%d: %s: %s", u.File, u.Line, u.Target, u.Why)
}

// Reasons a link cannot be carried.
const (
	whyTargetAbsent = "its target is not in the published set"
	whyAnchorAbsent = "the target page has no such heading"
)

// headingSlugs lists the ids a page's headings produce, by the same rule a
// Markdown renderer's table of contents uses: lowercase, punctuation dropped,
// whitespace runs collapsed to one hyphen, and a repeated heading suffixed in
// order. An explicit {#id} wins, because that is what it is for.
func headingSlugs(body string) map[string]bool {
	slugs := map[string]bool{}
	counts := map[string]int{}
	for line := range strings.Lines(body) {
		trimmed := strings.TrimSpace(line)
		hashes := 0
		for hashes < len(trimmed) && trimmed[hashes] == '#' {
			hashes++
		}
		if hashes == 0 || hashes == len(trimmed) || trimmed[hashes] != ' ' {
			continue
		}
		title := strings.TrimSpace(trimmed[hashes:])
		if open := strings.LastIndex(title, "{#"); open >= 0 && strings.HasSuffix(title, "}") {
			slugs[strings.TrimSuffix(title[open+2:], "}")] = true
			continue
		}
		slug := slugify(title)
		if slug == "" {
			continue
		}
		if counts[slug] > 0 {
			slug = fmt.Sprintf("%s_%d", slug, counts[slug])
		}
		counts[strings.SplitN(slug, "_", 2)[0]]++
		slugs[slug] = true
	}
	return slugs
}

// slugify is the table-of-contents id for one heading.
func slugify(title string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			if pending && b.Len() > 0 {
				b.WriteByte('-')
			}
			pending = false
			b.WriteRune(r)
		case unicode.IsSpace(r):
			pending = true
		}
	}
	return b.String()
}

// linkResolver rewrites one page's intra-docs links to the urls the artifact
// carries. It uses the same path-to-url rule as the pages themselves, so a link
// and the page it points at cannot disagree; only the anchor is carried over
// unchanged.
func linkResolver(relative string, urls map[string]string, slugs map[string]map[string]bool, sourceDir string) func(string) (string, string) {
	dir := path.Dir(relative)
	return func(target string) (string, string) {
		if target == "" {
			return "", whyTargetAbsent
		}
		// Anchors and anything with a scheme are not this tool's to resolve.
		if strings.HasPrefix(target, "#") || strings.Contains(target, "://") ||
			strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "//") {
			return target, ""
		}
		linkPath, anchor, _ := strings.Cut(target, "#")
		fragment := ""
		if anchor != "" {
			fragment = "#" + anchor
		}
		resolved := path.Clean(path.Join(dir, linkPath))
		if strings.HasSuffix(resolved, ".md") {
			url, ok := urls[resolved]
			if !ok {
				return "", whyTargetAbsent
			}
			// A fragment that matches no heading lands the reader at the top of a
			// long document with no indication why, so it is a finding rather than
			// a pass: the target resolving is not the same as the citation working.
			if anchor != "" && !slugs[resolved][anchor] {
				return "", whyAnchorAbsent
			}
			return url + fragment, ""
		}
		// An asset is served at its own path, when the set does not hold it back
		// and it is actually there.
		if !heldBack(resolved) {
			if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(resolved))); err == nil {
				return urlPrefix + resolved + fragment, ""
			}
		}
		return "", whyTargetAbsent
	}
}

// resolveLinks rewrites every intra-docs link in body and reports the ones whose
// target the published set holds back.
func resolveLinks(relative, body string, urls map[string]string, slugs map[string]map[string]bool, sourceDir string) (string, []unresolvedLink) {
	resolve := linkResolver(relative, urls, slugs, sourceDir)
	var unresolved []unresolvedLink
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		lines[index] = intraDocsLink.ReplaceAllStringFunc(line, func(match string) string {
			target := strings.TrimSpace(strings.TrimPrefix(match, "]("))
			replacement, why := resolve(target)
			if why != "" {
				unresolved = append(unresolved, unresolvedLink{File: relative, Line: index + 1, Target: target, Why: why})
				return match
			}
			return "](" + replacement
		})
	}
	return strings.Join(lines, "\n"), unresolved
}

// load reads every published page and derives the sidebar from the layout, which
// is how MkDocs derived it: a page added under a directory appears without a
// second edit anywhere.
func load(repoRoot string) (document, error) {
	sourceDir := filepath.Join(repoRoot, docsDir)
	bodies := map[string]string{}
	err := filepath.WalkDir(sourceDir, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		relative, err := filepath.Rel(sourceDir, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !published(relative) {
			return nil
		}
		body, err := os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("read %s: %w", relative, err)
		}
		bodies[relative] = string(body)
		return nil
	})
	if err != nil {
		return document{}, fmt.Errorf("walk %s: %w", docsDir, err)
	}
	if len(bodies) == 0 {
		return document{}, fmt.Errorf("no published pages under %s", docsDir)
	}

	// Every page's url first, so a link resolves against the whole set rather than
	// against whatever the walk had reached.
	urls := make(map[string]string, len(bodies))
	slugs := make(map[string]map[string]bool, len(bodies))
	for relative, body := range bodies {
		urls[relative] = pageURL(relative)
		slugs[relative] = headingSlugs(body)
	}

	var doc document
	var unresolved []unresolvedLink
	for relative, body := range bodies {
		resolved, missing := resolveLinks(relative, body, urls, slugs, sourceDir)
		unresolved = append(unresolved, missing...)
		doc.Pages = append(doc.Pages, page{
			URL:     urls[relative],
			Title:   pageTitle(relative, body),
			Section: sectionFor(relative),
			Body:    resolved,
		})
	}
	if len(unresolved) > 0 {
		sort.Slice(unresolved, func(i, j int) bool {
			if unresolved[i].File != unresolved[j].File {
				return unresolved[i].File < unresolved[j].File
			}
			return unresolved[i].Line < unresolved[j].Line
		})
		problems := make([]string, 0, len(unresolved))
		for _, link := range unresolved {
			problems = append(problems, link.Error())
		}
		return document{}, fmt.Errorf(
			"%d link(s) cannot be carried; see each reason:\n  %s",
			len(unresolved), strings.Join(problems, "\n  "))
	}

	sort.Slice(doc.Pages, func(i, j int) bool { return doc.Pages[i].URL < doc.Pages[j].URL })
	doc.Nav = navFor(doc.Pages)
	return doc, nil
}

// pageURL is the directory-style path a static site produces for one source file:
// docs/index.md is the root, docs/a/index.md is the section's own landing page,
// and docs/a/b.md is a page inside it.
func pageURL(relative string) string {
	if relative == "index.md" {
		return urlPrefix
	}
	dir, file := path.Split(relative)
	if file == "index.md" {
		return urlPrefix + dir // path.Split keeps the trailing slash
	}
	return urlPrefix + dir + strings.TrimSuffix(file, ".md") + "/"
}

// pageTitle is the page's own first heading, which is the title its author chose,
// falling back to its file name so a page without one is never nameless.
func pageTitle(relative, body string) string {
	for line := range strings.Lines(body) {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			if title := strings.TrimSpace(heading); title != "" {
				return title
			}
		}
	}
	return readableName(strings.TrimSuffix(path.Base(relative), ".md"))
}

// sectionFor is the group a page belongs to: its first directory, titled. A page
// directly under docs/ has no section, because it is not in one.
func sectionFor(relative string) string {
	segments := strings.Split(relative, "/")
	if len(segments) < 2 {
		return ""
	}
	return readableName(segments[0])
}

// acronyms are directory names whose natural label is not title case, so a
// sidebar does not read "Prds".
var acronyms = map[string]string{"prds": "PRDs"}

// readableName turns a directory or file name into a label.
func readableName(name string) string {
	if label, ok := acronyms[name]; ok {
		return label
	}
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

// navFor derives the sidebar tree from the pages. At each level the landing page
// comes first and the rest follow alphabetically, so a reader meets the section's
// own page before its children -- the order a static site's navigation uses.
func navFor(pages []page) []navNode {
	type node struct {
		title    string
		url      string
		children map[string]*node
	}
	root := &node{children: map[string]*node{}}
	for _, entry := range pages {
		segments := strings.Split(strings.Trim(strings.TrimPrefix(entry.URL, urlPrefix), "/"), "/")
		current := root
		for _, segment := range segments {
			if segment == "" {
				continue
			}
			child, ok := current.children[segment]
			if !ok {
				child = &node{title: readableName(segment), children: map[string]*node{}}
				current.children[segment] = child
			}
			current = child
		}
		if current == root {
			// The root index: the site's home, listed at the top of the sidebar.
			root.title, root.url = entry.Title, entry.URL
			continue
		}
		current.url, current.title = entry.URL, entry.Title
	}

	var render func(*node) []navNode
	render = func(parent *node) []navNode {
		names := make([]string, 0, len(parent.children))
		for name := range parent.children {
			names = append(names, name)
		}
		sort.Strings(names)

		// A directory whose own index is published: the section's landing page
		// leads with its link, then its children.
		var nodes []navNode
		for _, name := range names {
			child := parent.children[name]
			entry := navNode{Title: child.title}
			if child.url != "" {
				entry.URL = child.url
			}
			entry.Items = render(child)
			// A directory is a group whether it holds one page or twenty: the tree
			// mirrors the layout a reader sees, and collapsing a one-page directory
			// into a bare link would be a shape this artifact invented rather than
			// one it reports.
			nodes = append(nodes, entry)
		}
		return nodes
	}

	var nav []navNode
	if root.url != "" {
		nav = append(nav, navNode{Title: root.title, URL: root.url})
	}
	return append(nav, render(root)...)
}

// marshal renders the artifact. Indented and newline-terminated so a diff reads
// as a change to a page rather than a change to one line.
func marshal(doc document) ([]byte, error) {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode artifact: %w", err)
	}
	return append(encoded, '\n'), nil
}

// write regenerates the artifact, leaving the file untouched when its bytes
// already match so a no-op run does not churn the tree.
func write(repoRoot string) error {
	doc, err := load(repoRoot)
	if err != nil {
		return err
	}
	encoded, err := marshal(doc)
	if err != nil {
		return err
	}
	target := filepath.Join(repoRoot, artifactPath)
	existing, err := os.ReadFile(target)
	if err == nil && bytes.Equal(existing, encoded) {
		fmt.Printf("docsite: %s is up to date\n", artifactPath)
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", artifactPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(artifactPath), err)
	}
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", artifactPath, err)
	}
	fmt.Printf("docsite: wrote %s (%d pages)\n", artifactPath, len(doc.Pages))
	return nil
}

// check regenerates into a temporary directory and compares, so a source edit that
// has not been reflected in the committed artifact fails without the tree being
// touched.
func check(repoRoot string) error {
	tmpDir, err := os.MkdirTemp("", "docsite-check-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	generated, err := load(repoRoot)
	if err != nil {
		return err
	}
	expected, err := marshal(generated)
	if err != nil {
		return err
	}
	committed, err := os.ReadFile(filepath.Join(repoRoot, artifactPath))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s is missing; regenerate it by running `go -C tools run ./docsite --repo-root ..` from the tools module", artifactPath)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", artifactPath, err)
	}
	if bytes.Equal(expected, committed) {
		fmt.Printf("docsite: %s is up to date (%d pages)\n", artifactPath, len(generated.Pages))
		return nil
	}
	return fmt.Errorf("%s is stale; regenerate it by running `go -C tools run ./docsite --repo-root ..` from the tools module", artifactPath)
}
