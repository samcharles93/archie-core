// Command prdlint rejects session-bound writing in docs/prds.
//
// A PRD states a decision, a requirement, a constraint, or evidence
// (docs/prds/RULES.md). Those survive the code moving underneath them. A line
// number, a progress report, or a dated status claim does not: it is true on
// the day it is written and silently wrong a month later, while still reading
// as authoritative. The tracker records progress; the PRD records the design.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultDir is the directory prdlint governs, relative to the repository root.
const defaultDir = "docs/prds"

func main() {
	flags := flag.NewFlagSet("prdlint", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repoRoot := flags.String("repo-root", "..", "path to the Archie repository root")
	dir := flags.String("dir", defaultDir, "directory to lint, relative to the repository root")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "prdlint: %v\n", err)
		os.Exit(2)
	}
	if extra := flags.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "prdlint: unexpected argument %q\n", extra[0])
		os.Exit(2)
	}

	findings, err := lintDir(filepath.Join(*repoRoot, *dir))
	if err != nil {
		fmt.Fprintf(os.Stderr, "prdlint: %v\n", err)
		os.Exit(2)
	}
	if len(findings) == 0 {
		fmt.Printf("prdlint: %s carries no session-bound writing\n", *dir)
		return
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f.String())
	}
	fmt.Fprintf(os.Stderr, "\nprdlint: %d finding(s). A PRD outlives the code it describes:\n%s\n",
		len(findings), strings.Join(remedies(findings), "\n"))
	os.Exit(1)
}

// lintDir lints every Markdown file in dir, skipping RULES.md: it is the rule
// text itself and quotes the shapes the rules forbid.
func lintDir(dir string) ([]Finding, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist", dir)
	}
	if err != nil {
		return nil, err
	}

	var findings []Finding
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" || e.Name() == "RULES.md" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		findings = append(findings, Lint(name, string(data))...)
	}
	return findings, nil
}

// remedies renders each triggered rule's fix once, in rule order, so the
// output ends with instructions rather than repeating them per finding.
func remedies(findings []Finding) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rules {
		for _, f := range findings {
			if f.Rule == r.name && !seen[r.name] {
				seen[r.name] = true
				out = append(out, "  "+r.name+": "+r.remedy)
			}
		}
	}
	return out
}
