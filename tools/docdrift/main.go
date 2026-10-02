// Command docdrift rejects documentation that cites a repository path which no
// longer exists.
//
// A page under docs/ states a design, a contract, or a decision, and cites the
// code that carries it. When that code moves or is deleted the sentence
// survives unchanged: it still reads as authoritative, and every session told
// to treat docs/architecture as settled design pays for it. Nothing else in the
// gate compares a prose citation against the tree. docsgen checks the contract
// schema against Go types, docsite checks generated artifacts against their
// Markdown sources, prdlint checks PRD writing style. All three verify internal
// consistency, so a page can describe a package deleted months ago and every
// check stays green -- which is how a page describing a removed system survives
// behind a hand-written "removed" banner.
//
// The baseline is a burn-down list, not a licence. A citation listed there is
// known debt: repoint it or delete the claim, then remove the line. The check
// fails when an allowlisted citation starts resolving again, so the list cannot
// quietly outlive its reasons.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultDir      = "docs"
	defaultBaseline = "tools/docdrift/baseline.txt"
)

// citationRE matches a repository path cited in prose. The character class
// stops at backticks, brackets, commas and whitespace, so Markdown around the
// citation does not leak into the match.
var citationRE = regexp.MustCompile(`\binternal/[A-Za-z0-9_./-]+`)

// suffixRE strips the line-number suffix some older prose carries.
var suffixRE = regexp.MustCompile(`:\d+$`)

// citation is one prose reference to a repository path.
type citation struct {
	Path string // normalized, e.g. internal/config, internal/daemon/daemon.go
	File string // repo-relative file it appears in
	Line int    // 1-based
}

func (c citation) String() string {
	return fmt.Sprintf("%s:%d: %s", c.File, c.Line, c.Path)
}

// analysis is the outcome of comparing the tree against the baseline.
type analysis struct {
	newDrift  []citation // unresolvable and not allowlisted
	stale     []string   // allowlisted but resolving again
	unused    []string   // allowlisted but no longer cited anywhere
	known     int        // unresolvable and allowlisted
	scanned   int        // citations that do resolve
	citedFile int        // files containing at least one citation
}

func main() {
	args := os.Args[1:]
	mode := "check"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
		args = args[1:]
	}

	flags := flag.NewFlagSet("docdrift", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repoRoot := flags.String("repo-root", "..", "path to the Archie repository root")
	dir := flags.String("dir", defaultDir, "directory to scan, relative to the repository root")
	baselinePath := flags.String("baseline", defaultBaseline, "baseline file, relative to the repository root")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
		os.Exit(2)
	}
	if extra := flags.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "docdrift: unexpected argument %q\n", extra[0])
		os.Exit(2)
	}

	root, err := filepath.Abs(*repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
		os.Exit(2)
	}

	switch mode {
	case "check":
		if err := runCheck(root, *dir, filepath.Join(root, *baselinePath)); err != nil {
			fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
			os.Exit(2)
		}
	case "list":
		if err := runList(root, *dir); err != nil {
			fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "docdrift: unknown mode %q (want check or list)\n", mode)
		os.Exit(2)
	}
}

// runCheck reports new drift and stale baseline entries, exiting 1 on either.
func runCheck(root, dir, baselinePath string) error {
	baseline, err := readBaseline(baselinePath)
	if err != nil {
		return err
	}
	result, err := analyse(root, dir, baseline)
	if err != nil {
		return err
	}

	for _, c := range result.newDrift {
		fmt.Fprintf(os.Stderr, "new drift: %s\n", c)
	}
	for _, p := range result.stale {
		fmt.Fprintf(os.Stderr, "stale baseline entry (now resolves, remove it): %s\n", p)
	}
	for _, p := range result.unused {
		fmt.Fprintf(os.Stderr, "note: baseline entry no longer cited anywhere: %s\n", p)
	}

	if len(result.newDrift) == 0 && len(result.stale) == 0 {
		fmt.Printf("docdrift: %d cited file(s), %d resolving citation(s), %d known-debt citation(s)\n",
			result.citedFile, result.scanned, result.known)
		return nil
	}
	fmt.Fprintf(os.Stderr,
		"\ndocdrift: %d new dangling citation(s), %d stale baseline entry(ies).\n"+
			"Repoint the citation at the code that carries it now, delete the claim it\n"+
			"supported, or -- only for a path that is knowingly gone -- add it to %s\n"+
			"with the reason in the commit message.\n",
		len(result.newDrift), len(result.stale), defaultBaseline)
	os.Exit(1)
	return nil
}

// runList prints the current debt in baseline format, for regeneration.
func runList(root, dir string) error {
	cites, err := scan(root, dir)
	if err != nil {
		return err
	}
	fmt.Println("# docdrift baseline: citations in docs/ that no longer resolve to a")
	fmt.Println("# repository path. This is a burn-down list, not a licence -- repoint the")
	fmt.Println("# citation or delete the claim, then remove the line. Regenerate with:")
	fmt.Println("#")
	fmt.Println("#   go -C tools run -mod=readonly ./docdrift list --repo-root .. > tools/docdrift/baseline.txt")
	seen := map[string]bool{}
	for _, c := range cites {
		if seen[c.Path] || resolves(root, c.Path) {
			continue
		}
		seen[c.Path] = true
		fmt.Println(c.Path)
	}
	return nil
}

// analyse compares every citation in dir against the tree and the baseline.
func analyse(root, dir string, baseline map[string]bool) (analysis, error) {
	cites, err := scan(root, dir)
	if err != nil {
		return analysis{}, err
	}
	var out analysis
	unresolved := map[string]bool{}
	files := map[string]bool{}
	for _, c := range cites {
		files[c.File] = true
		if resolves(root, c.Path) {
			out.scanned++
			continue
		}
		unresolved[c.Path] = true
		if baseline[c.Path] {
			out.known++
			continue
		}
		out.newDrift = append(out.newDrift, c)
	}
	out.citedFile = len(files)
	for p := range baseline {
		switch {
		case resolves(root, p):
			out.stale = append(out.stale, p)
		case !unresolved[p]:
			out.unused = append(out.unused, p)
		}
	}
	sort.Strings(out.stale)
	sort.Strings(out.unused)
	return out, nil
}

// scan collects every citation in the Markdown files under dir.
func scan(root, dir string) ([]citation, error) {
	base := filepath.Join(root, dir)
	var out []citation
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Generated artifacts embed page bodies; scanning them would
			// double-count every citation they carry.
			if d.Name() == "generated" || d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		return scanFile(root, path, &out)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("scan %s: %w", dir, err)
	}
	return out, err
}

// scanFile appends every citation found in one file, with its line number.
func scanFile(root, path string, out *[]citation) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		for _, raw := range citationRE.FindAllString(scanner.Text(), -1) {
			p := normalize(raw)
			if p == "" {
				continue
			}
			*out = append(*out, citation{Path: p, File: rel, Line: line})
		}
	}
	return scanner.Err()
}

// normalize strips the Markdown and Go-pattern decoration a prose citation
// carries, leaving a repository path.
func normalize(raw string) string {
	s := strings.TrimRight(raw, ".,;:/")
	s = strings.TrimSuffix(s, "/...")
	s = suffixRE.ReplaceAllString(s, "")
	return strings.TrimRight(s, ".,;:/")
}

// resolves reports whether a citation names something that exists in the tree:
// a file, a directory, or a package directory cited with a symbol attached.
func resolves(root, path string) bool {
	for _, c := range candidates(path) {
		if c == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, c)); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(root, c+".go")); err == nil {
			return true
		}
		if prefixMatch(root, c) {
			return true
		}
	}
	return false
}

// prefixMatch reports whether a citation names a file shape rather than a file.
// `internal/webui/api_<concern>.go` documents a naming convention: the angle
// bracket ends the path token, leaving a truncated final segment. It is
// satisfied when a sibling starts with that segment.
func prefixMatch(root, path string) bool {
	dir, base := filepath.Split(path)
	if !strings.HasSuffix(base, "_") {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base) {
			return true
		}
	}
	return false
}

// candidates lists the paths a citation could mean: itself, itself with a
// trailing ".Symbol" dropped (a package cited together with a type or
// function), and itself with a trailing "/Symbol" dropped (a test or function
// cited with its package).
func candidates(path string) []string {
	out := []string{path}
	if strings.HasSuffix(path, ".go") {
		return out
	}
	if i := strings.LastIndex(path, "."); i > strings.LastIndex(path, "/") {
		out = append(out, path[:i])
	}
	if i := strings.LastIndex(path, "/"); i > 0 && isSymbolSegment(path[i+1:]) {
		out = append(out, path[:i])
	}
	return out
}

// isSymbolSegment reports whether a path segment names a Go identifier rather
// than a package directory.
func isSymbolSegment(seg string) bool {
	if seg == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(seg)
	return unicode.IsUpper(r)
}

// readBaseline loads the allowlist: one citation per line, "#" comments.
func readBaseline(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer f.Close()

	out := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, scanner.Err()
}
