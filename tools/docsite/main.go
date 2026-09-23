// Command docsite derives the documentation sets into canonical, host-neutral
// artifacts.
//
// The Markdown is the source; the JSON is the fact set every renderer reads.
// MkDocs was the first renderer and the Astro app is the second, so the artifact
// is the product and the Markdown stays the source -- tools/newsgen states the
// same principle for releases, where the JSON is canonical and the Markdown is an
// adapter.
//
// One source tree yields two sets, each with its own artifact and its own url
// base: see publishRules for the split and the reasons. A renderer reads both
// files and serves each under its own base; the file a page arrives in is the set
// it belongs to, so neither carries a discriminator.
//
// Output is generated and committed:
//
//	docs/data/generated/docs.json
//	docs/data/generated/dev-docs.json
//
// `docsite check` regenerates both in memory and fails when a committed one
// differs, without touching the tree.
//
// This tool keeps no registry of the generated directory it writes into: that
// directory is accounted for by docsgen's generatedArtifacts, which names both
// files, so a second registry here would be a second home for one fact.
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
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// docsDir is the Markdown source tree, relative to the repository root.
const docsDir = "docs"

// docSet is one published set: the pages it holds, the path they are served
// under, and the committed artifact this tool writes for it.
type docSet struct {
	Name         string
	URLPrefix    string
	ArtifactPath string
}

// The two sets. The public set keeps the directory-style paths MkDocs produced
// exactly, so existing links keep working.
var (
	publicDocs      = docSet{Name: "public", URLPrefix: "/docs/", ArtifactPath: "docs/data/generated/docs.json"}
	developmentDocs = docSet{Name: "development", URLPrefix: "/dev/", ArtifactPath: "docs/data/generated/dev-docs.json"}

	// sets is the write and check order, so a run reports its files the same way
	// every time.
	sets = []docSet{publicDocs, developmentDocs}
)

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

// publishRules is the split, and the reasons. Everything under docs/ publishes,
// in one set or the other, except:
//
//   - archive/     superseded design. Publishing it competes with the current
//     architecture for a reader's attention, which is the exact failure this
//     repository already fights.
//   - inspiration/ research notes that discuss other products by name.
//   - news/        release notes, which tools/newsgen owns and a renderer reads
//     from releases.json.
//   - github-token.md  setup notes about credentials.
//
// What remains divides by audience, not by sensitivity: every development page
// may be read by anyone. architecture/ and prds/ are written for contributors --
// the organisation rules, the migration register, the review procedure, the open
// design decisions -- and a reader looking for what Archie is and how to point an
// event at it is not served by meeting them in one sidebar. They are the
// development set. Everything else is the public set.
//
// Selective PRD publication is gone with the split rather than carried into it: it
// existed only because settled architecture cites PRDs and a link to a held-back
// target fails this tool, so five PRDs published to keep citations alive. Both
// halves of those citations are now in the development set.
//
// Changing either set means editing this list. The comment is the record: a
// renderer that reads an artifact cannot see why a page is absent from it, and
// neither can the next person deciding where a new page belongs.
var (
	heldBackTrees   = map[string]bool{"archive": true, "inspiration": true, "news": true}
	heldBackPages   = map[string]bool{"github-token.md": true}
	developmentTree = map[string]bool{"architecture": true, "prds": true}
)

// setFor is the set one published page belongs to, by its top-level directory.
func setFor(relative string) docSet {
	if developmentTree[strings.Split(relative, "/")[0]] {
		return developmentDocs
	}
	return publicDocs
}

// heldBack reports whether one path relative to docs/ is deliberately absent from
// the published set. Links and pages are judged by this one rule, so a page is
// never published while a link to it is refused, or the reverse.
func heldBack(relative string) bool {
	if heldBackPages[relative] {
		return true
	}
	return heldBackTrees[strings.Split(relative, "/")[0]]
}

// published reports whether one path relative to docs/ is a page either artifact
// carries.
func published(relative string) bool {
	return strings.HasSuffix(relative, ".md") && !heldBack(relative)
}

// intraDocsLink matches a Markdown inline link or image. The target is the only
// part this tool rewrites; everything else in the match is preserved.
var intraDocsLink = regexp.MustCompile(`\]\(\s*([^)\s]+)`)

// fenceDelimiter opens or closes a fenced code block.
var fenceDelimiter = regexp.MustCompile("^\\s*(```|~~~)")

// maskCode blanks the code spans in one line, keeping its length and every other
// byte, so a link match found in the masked line indexes the real one. Code is
// prose to a Markdown renderer's link parser: `Resolve[T](i, src)` is a generic
// call, not a link to `i,`, and the difference is the backticks.
func maskCode(line string) string {
	masked := []byte(line)
	for index := 0; index < len(masked); {
		if masked[index] != '`' {
			index++
			continue
		}
		open := index
		for index < len(masked) && masked[index] == '`' {
			index++
		}
		run := index - open
		closed := -1
		for scan := index; scan < len(masked); {
			if masked[scan] != '`' {
				scan++
				continue
			}
			start := scan
			for scan < len(masked) && masked[scan] == '`' {
				scan++
			}
			if scan-start == run {
				closed = scan
				break
			}
		}
		// An unclosed run is literal text to a Markdown renderer, so it is literal
		// here too and the scan continues past it.
		if closed < 0 {
			continue
		}
		for blank := open; blank < closed; blank++ {
			masked[blank] = ' '
		}
		index = closed
	}
	return string(masked)
}

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
		// An asset is served at its own path under the public base, whichever set
		// links to it: docs/public/ is one file set, and serving one file under two
		// paths would invent a second location for it.
		if !heldBack(resolved) {
			if _, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(resolved))); err == nil {
				return publicDocs.URLPrefix + resolved + fragment, ""
			}
		}
		return "", whyTargetAbsent
	}
}

// resolveLinks rewrites every intra-docs link in body and reports the ones whose
// target neither set carries. Code is skipped, span and block alike: what a
// Markdown renderer draws as a link is the only thing this tool may rewrite, and
// the only thing it may refuse a page for.
func resolveLinks(relative, body string, urls map[string]string, slugs map[string]map[string]bool, sourceDir string) (string, []unresolvedLink) {
	resolve := linkResolver(relative, urls, slugs, sourceDir)
	var unresolved []unresolvedLink
	lines := strings.Split(body, "\n")
	inFence := false
	for index, line := range lines {
		if fenceDelimiter.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		var rewritten strings.Builder
		end := 0
		for _, match := range intraDocsLink.FindAllStringSubmatchIndex(maskCode(line), -1) {
			target := line[match[2]:match[3]]
			replacement, why := resolve(target)
			if why != "" {
				unresolved = append(unresolved, unresolvedLink{File: relative, Line: index + 1, Target: target, Why: why})
				continue
			}
			rewritten.WriteString(line[end:match[2]])
			rewritten.WriteString(replacement)
			end = match[3]
		}
		if end == 0 {
			continue
		}
		rewritten.WriteString(line[end:])
		lines[index] = rewritten.String()
	}
	return strings.Join(lines, "\n"), unresolved
}

// readPages reads the source of every page either set carries, keyed by its path
// relative to docs/.
//
// The page list comes from the commit, not from the working tree: see
// sourcePages for why that distinction is the whole of this function's contract.
func readPages(repoRoot, sourceDir string) (map[string]string, error) {
	relativePaths, err := sourcePages(repoRoot, sourceDir)
	if err != nil {
		return nil, err
	}
	bodies := map[string]string{}
	for _, relative := range relativePaths {
		if !published(relative) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(sourceDir, relative))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", relative, err)
		}
		bodies[relative] = string(body)
	}
	if len(bodies) == 0 {
		return nil, fmt.Errorf("no published pages under %s", docsDir)
	}
	return bodies, nil
}

// sourcePages lists the published candidate pages, relative to docs/, choosing
// git over a directory walk whenever git can answer.
//
// The artifacts are committed beside their sources, so each one must be derived
// from the files the commit carries. A walk also sees an UNTRACKED page -- one no
// commit holds -- and then reported the artifact as stale, which sent the reader
// to regenerate an artifact that was already correct and would have committed a
// page belonging to no commit. That happened twice in one session, once to a
// migration branch and once to an unrelated writer staging a new PRD.
//
// The index counts as well as HEAD, deliberately: a page staged for this commit
// is about to be carried by it, so the artifact must include it and check must
// fail until it does. Only a file in neither tree is ignored -- and that is
// exactly the working-tree scratch this exists to exclude.
//
// Outside a git work tree there is no commit to disagree with, so the walk is
// the best answer available and is used unchanged, with a line saying so rather
// than a silence that reads as the stronger guarantee.
func sourcePages(repoRoot, sourceDir string) ([]string, error) {
	pages, err := trackedPages(repoRoot, sourceDir)
	if err == nil {
		return pages, nil
	}
	fmt.Fprintf(os.Stderr, "docsite: %v; falling back to a directory walk, which cannot tell an untracked page from a committed one\n", err)
	return walkedPages(sourceDir)
}

// trackedPages asks git for the .md files under docs/ that the index or HEAD
// carries.
func trackedPages(repoRoot, sourceDir string) ([]string, error) {
	rel, err := filepath.Rel(repoRoot, sourceDir)
	if err != nil {
		return nil, fmt.Errorf("locate %s under %s: %w", docsDir, repoRoot, err)
	}
	rel = filepath.ToSlash(rel)
	// -z because a path may contain anything but NUL; --cached so a page staged
	// for this commit counts.
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "--cached", "-z", "--", rel).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files %s: %w", rel, err)
	}
	var pages []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		relative := strings.TrimPrefix(entry, rel+"/")
		if relative == entry || !strings.HasSuffix(relative, ".md") {
			continue
		}
		pages = append(pages, relative)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("git ls-files %s listed no Markdown", rel)
	}
	return pages, nil
}

// walkedPages is the gitless enumeration: every .md under the tree, tracked or
// not.
func walkedPages(sourceDir string) ([]string, error) {
	var pages []string
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
		pages = append(pages, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", docsDir, err)
	}
	return pages, nil
}

// setDocument pairs one set with the artifact generated for it.
type setDocument struct {
	Set      docSet
	Document document
}

// load reads every published page and derives each set's sidebar from the layout,
// which is how MkDocs derived it: a page added under a directory appears without a
// second edit anywhere. Both sets are built in one pass over one url map, so a
// link that crosses between them resolves to the base its target is served under.
func load(repoRoot string) ([]setDocument, error) {
	sourceDir := filepath.Join(repoRoot, docsDir)
	bodies, err := readPages(repoRoot, sourceDir)
	if err != nil {
		return nil, err
	}

	// Every page's url first, so a link resolves against the whole set rather than
	// against whatever the walk had reached.
	urls := make(map[string]string, len(bodies))
	slugs := make(map[string]map[string]bool, len(bodies))
	for relative, body := range bodies {
		urls[relative] = pageURL(relative)
		slugs[relative] = headingSlugs(body)
	}

	pages := map[string][]page{}
	var unresolved []unresolvedLink
	for relative, body := range bodies {
		resolved, missing := resolveLinks(relative, body, urls, slugs, sourceDir)
		unresolved = append(unresolved, missing...)
		set := setFor(relative)
		pages[set.Name] = append(pages[set.Name], page{
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
		return nil, fmt.Errorf(
			"%d link(s) cannot be carried; see each reason:\n  %s",
			len(unresolved), strings.Join(problems, "\n  "))
	}

	generated := make([]setDocument, 0, len(sets))
	for _, set := range sets {
		entries := pages[set.Name]
		if len(entries) == 0 {
			return nil, fmt.Errorf("no pages in the %s set", set.Name)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].URL < entries[j].URL })
		generated = append(generated, setDocument{
			Set:      set,
			Document: document{Pages: entries, Nav: navFor(entries, set.URLPrefix)},
		})
	}
	return generated, nil
}

// pageURL is the directory-style path a static site produces for one source file,
// under the base of the set the file belongs to: docs/index.md is the public
// root, docs/a/index.md is a section's own landing page, and docs/a/b.md is a page
// inside it.
func pageURL(relative string) string {
	prefix := setFor(relative).URLPrefix
	if relative == "index.md" {
		return prefix
	}
	dir, file := path.Split(relative)
	if file == "index.md" {
		return prefix + dir // path.Split keeps the trailing slash
	}
	return prefix + dir + strings.TrimSuffix(file, ".md") + "/"
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
func navFor(pages []page, urlPrefix string) []navNode {
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

// write regenerates every artifact, leaving a file untouched when its bytes
// already match so a no-op run does not churn the tree.
func write(repoRoot string) error {
	generated, err := load(repoRoot)
	if err != nil {
		return err
	}
	for _, entry := range generated {
		if err := writeSet(repoRoot, entry); err != nil {
			return err
		}
	}
	return nil
}

func writeSet(repoRoot string, entry setDocument) error {
	artifactPath := entry.Set.ArtifactPath
	encoded, err := marshal(entry.Document)
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
	fmt.Printf("docsite: wrote %s (%d pages)\n", artifactPath, len(entry.Document.Pages))
	return nil
}

// check regenerates in memory and compares, so a source edit that has not been
// reflected in a committed artifact fails without the tree being touched.
func check(repoRoot string) error {
	generated, err := load(repoRoot)
	if err != nil {
		return err
	}
	for _, entry := range generated {
		if err := checkSet(repoRoot, entry); err != nil {
			return err
		}
	}
	return nil
}

const regenerate = "regenerate it by running `go -C tools run ./docsite --repo-root ..` from the tools module"

func checkSet(repoRoot string, entry setDocument) error {
	artifactPath := entry.Set.ArtifactPath
	expected, err := marshal(entry.Document)
	if err != nil {
		return err
	}
	committed, err := os.ReadFile(filepath.Join(repoRoot, artifactPath))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s is missing; %s", artifactPath, regenerate)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", artifactPath, err)
	}
	if !bytes.Equal(expected, committed) {
		return fmt.Errorf("%s is stale; %s", artifactPath, regenerate)
	}
	fmt.Printf("docsite: %s is up to date (%d pages)\n", artifactPath, len(entry.Document.Pages))
	return nil
}
