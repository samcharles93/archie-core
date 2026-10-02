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
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
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
	os.Exit(run())
}

// run holds the whole program so its deferred signal cleanup actually runs:
// os.Exit in main cannot be deferred past, and this tool's exit codes carry the
// gate's verdict, so the code is propagated rather than exited on.
func run() int {
	// The one subprocess this tool runs (git ls-files) is registered against
	// this context, so a cancelled gate does not leave a git process behind and
	// a wedged one cannot outlive the run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		return 2
	}
	if extra := flags.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "docdrift: unexpected argument %q\n", extra[0])
		return 2
	}

	root, err := filepath.Abs(*repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
		return 2
	}

	tree, err := newTrackedTree(ctx, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
		return 2
	}

	switch mode {
	case "check":
		failed, err := runCheck(root, *dir, tree, filepath.Join(root, *baselinePath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
			return 2
		}
		if failed {
			return 1
		}
		return 0
	case "list":
		if err := runList(root, *dir, tree); err != nil {
			fmt.Fprintf(os.Stderr, "docdrift: %v\n", err)
			return 2
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "docdrift: unknown mode %q (want check or list)\n", mode)
		return 2
	}
}

// runCheck reports new drift and stale baseline entries. It answers whether the
// tree fails the gate, and returns an error only when the check itself could not
// run -- a distinction the exit code carries.
func runCheck(root, dir string, tree *trackedTree, baselinePath string) (bool, error) {
	baseline, err := readBaseline(baselinePath)
	if err != nil {
		return false, err
	}
	result, err := analyse(root, dir, tree, baseline)
	if err != nil {
		return false, err
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
		return false, nil
	}
	fmt.Fprintf(os.Stderr,
		"\ndocdrift: %d new dangling citation(s), %d stale baseline entry(ies).\n"+
			"Repoint the citation at the code that carries it now, delete the claim it\n"+
			"supported, or -- only for a path that is knowingly gone -- add it to %s\n"+
			"with the reason in the commit message.\n",
		len(result.newDrift), len(result.stale), defaultBaseline)
	return true, nil
}

// runList prints the current debt in baseline format, for regeneration.
func runList(root, dir string, tree *trackedTree) error {
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
		if seen[c.Path] || tree.resolves(c.Path) {
			continue
		}
		seen[c.Path] = true
		fmt.Println(c.Path)
	}
	return nil
}

// analyse compares every citation in dir against the tree and the baseline.
func analyse(root, dir string, tree *trackedTree, baseline map[string]bool) (analysis, error) {
	cites, err := scan(root, dir)
	if err != nil {
		return analysis{}, err
	}
	var out analysis
	unresolved := map[string]bool{}
	files := map[string]bool{}
	for _, c := range cites {
		files[c.File] = true
		if tree.resolves(c.Path) {
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
		case tree.resolves(p):
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

// trackedTree is the set of paths git tracks: what "exists" means to this gate.
//
// It is deliberately not the filesystem. A directory left behind by a checkout
// that removed its files, or by a branch switch, exists on one machine and not
// another, so a filesystem check makes this gate answer differently in CI and in
// a developer's tree. That is how it first shipped a baseline that passed
// locally and failed in CI: five citations -- internal/gate, internal/gate/gateeval
// and internal/domain/workflow/skillbuild among them -- resolved against empty
// leftover directories that were never in the repository at all.
type trackedTree struct {
	files map[string]bool
	dirs  map[string]bool
}

// newTrackedTree reads the tracked tree from git. Failing to read it is fatal
// rather than a fallback to the filesystem, because a silent fallback is the
// non-reproducibility this type exists to remove.
func newTrackedTree(ctx context.Context, root string) (*trackedTree, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w (docdrift resolves against the tracked tree, so it must run in a git checkout)", err)
	}
	tree := &trackedTree{files: map[string]bool{}, dirs: map[string]bool{}}
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		tree.files[path] = true
		// Every ancestor of a tracked file is a directory the repository has,
		// which is what makes `internal/domain/workflow` resolve without a
		// file being named.
		for dir := filepath.Dir(path); dir != "." && dir != "/" && dir != ""; dir = filepath.Dir(dir) {
			tree.dirs[dir] = true
		}
	}
	return tree, nil
}

// resolves reports whether a citation names something the repository holds: a
// tracked file, a directory containing tracked files, or a package directory
// cited with a symbol attached.
func (t *trackedTree) resolves(path string) bool {
	for _, c := range candidates(path) {
		if c == "" {
			continue
		}
		if t.files[c] || t.files[c+".go"] || t.dirs[c] {
			return true
		}
		if t.prefixMatch(c) {
			return true
		}
	}
	return false
}

// prefixMatch reports whether a citation names a file shape rather than a file.
// `internal/webui/api_<concern>.go` documents a naming convention: the angle
// bracket ends the path token, leaving a truncated final segment. It is
// satisfied when a tracked sibling starts with that segment.
func (t *trackedTree) prefixMatch(path string) bool {
	dir, base := filepath.Split(path)
	if !strings.HasSuffix(base, "_") {
		return false
	}
	dir = strings.TrimSuffix(dir, "/")
	for file := range t.files {
		if filepath.Dir(file) == dir && strings.HasPrefix(filepath.Base(file), base) {
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
