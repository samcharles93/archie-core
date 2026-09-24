// Command newsgen derives every published release fact from CHANGELOG.md.
//
// CHANGELOG.md is the release process's frozen notes: tools/release.sh
// --prepare writes a section, the maintainer edits it, and --tag refuses to tag
// a version without its section. Tags are deliberately not a source, because CI
// and deployment check out shallow and a site build has no tags to read.
//
// Generated and committed artifacts:
//
//	docs/news/releases.json      the canonical, host-neutral fact set
//	docs/news/redirects.json     the retired per-component URLs, mapped to pages
//
// `newsgen check` reproduces the output in memory and fails when the committed
// files differ, which keeps a changelog edit from landing without updating the
// artifacts consumed by the external documentation site.
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

// load parses the changelog and returns the releases newest first.
func load(repoRoot string) ([]release, error) {
	source, err := os.ReadFile(filepath.Join(repoRoot, changelogPath))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", changelogPath, err)
	}
	releases, err := parseChangelog(string(source))
	if err != nil {
		return nil, err
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
			problems = append(problems, path+" does not match the changelog")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("release notes are stale:\n  %s\n  fix with `task news`, then commit the result",
			strings.Join(problems, "\n  "))
	}
	fmt.Printf("newsgen: %d release notes match the changelog\n", len(releases))
	return nil
}
