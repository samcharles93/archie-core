// Command newsgen derives every published release fact from the component
// changelogs.
//
// CHANGELOG.archied.md and CHANGELOG.archie.md are the release process's frozen
// notes: tools/release.sh --prepare writes a section, the maintainer edits it,
// and --tag refuses to tag a version without its section. Tags are deliberately
// not a source, because CI and deployment check out shallow and a site build has
// no tags to read.
//
// Output is generated and committed:
//
//	docs/data/generated/releases.json   the canonical, host-neutral fact set
//	docs/news/index.md                  the news page
//	docs/news/<component>/<version>.md  one page per release
//
// `newsgen check` reproduces the output in memory and fails when the committed
// files differ, which keeps a changelog edit from landing without its pages.
// `newsgen` rewrites only files it owns and leaves every other file in
// docs/news alone.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		log.Fatalf("newsgen: %v", err)
	}
	if parsed.mode == modeCheck {
		err = check(parsed.repoRoot)
	} else {
		err = write(parsed.repoRoot)
	}
	if err != nil {
		log.Fatalf("newsgen: %v", err)
	}
}

// parseOptions resolves the optional `check` subcommand and the flags that
// follow it. A leftover positional argument is an error rather than something
// to ignore: `newsgen --repo-root .. check` would otherwise fall through to the
// write path and rewrite the files that check promises not to touch.
func parseOptions(args []string) (options, error) {
	selected := modeWrite
	if len(args) > 0 && args[0] == "check" {
		selected = modeCheck
		args = args[1:]
	}
	flags := flag.NewFlagSet("newsgen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repoRoot := flags.String("repo-root", "..", "path to the repository root")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if extra := flags.Args(); len(extra) > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", extra[0])
	}
	return options{mode: selected, repoRoot: *repoRoot}, nil
}

// load parses every component changelog and returns the releases newest first.
func load(repoRoot string) ([]release, error) {
	releases := []release{}
	for _, c := range components {
		source, err := os.ReadFile(filepath.Join(repoRoot, c.changelog))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", c.changelog, err)
		}
		parsed, err := parseChangelog(c, string(source))
		if err != nil {
			return nil, err
		}
		releases = append(releases, parsed...)
	}
	sortReleases(releases)
	return releases, nil
}

// write regenerates the owned files, skipping any whose bytes already match so
// a re-run cannot dirty a release commit.
func write(repoRoot string) error {
	releases, err := load(repoRoot)
	if err != nil {
		return err
	}
	files, err := render(releases)
	if err != nil {
		return err
	}
	updated := 0
	for path, body := range files {
		abs := filepath.Join(repoRoot, filepath.FromSlash(path))
		current, err := os.ReadFile(abs)
		switch {
		case err == nil && bytes.Equal(current, body):
			continue
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(abs, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		updated++
	}
	fmt.Printf("newsgen: %d releases, %d files updated\n", len(releases), updated)
	return nil
}

// check reports every difference between the committed files and a fresh
// derivation, without touching the working tree.
func check(repoRoot string) error {
	releases, err := load(repoRoot)
	if err != nil {
		return err
	}
	files, err := render(releases)
	if err != nil {
		return err
	}
	var problems []string
	for path, expected := range files {
		current, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problems = append(problems, path+" is missing")
			continue
		case err != nil:
			return fmt.Errorf("read %s: %w", path, err)
		}
		if !bytes.Equal(current, expected) {
			problems = append(problems, path+" does not match the changelogs")
		}
	}
	for _, path := range unownedPages(repoRoot, files) {
		problems = append(problems, path+" is not generated; move it out of the generated directory")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("release notes are stale:\n  %s\n  fix with `task news`, then commit the result",
			strings.Join(problems, "\n  "))
	}
	fmt.Printf("newsgen: %d release notes match the changelogs\n", len(releases))
	return nil
}

// unownedPages lists files inside a generated component directory that this
// tool does not produce. The news directory's own root is not a generated
// directory, so a hand-written announcement there is left alone.
func unownedPages(repoRoot string, files map[string][]byte) []string {
	var unowned []string
	for _, c := range components {
		dir := filepath.Join(repoRoot, newsDir, c.key)
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			slash := filepath.ToSlash(relative)
			if _, generated := files[slash]; !generated {
				unowned = append(unowned, slash)
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			unowned = append(unowned, fmt.Sprintf("%s is unreadable: %v", c.key, err))
		}
	}
	return unowned
}
