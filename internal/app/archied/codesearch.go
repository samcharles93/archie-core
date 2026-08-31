package archied

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/samcharles93/archie-core/internal/indexing"
)

// flagErrorOutput receives helper diagnostics. It is a variable so tests can
// capture them, and it is deliberately not stdout: the parent parses stdout
// as JSON, so a stray usage message there would corrupt the result.
var flagErrorOutput io.Writer = os.Stderr

// codesearchHelperCommand is the argv[1] token that switches archied out of
// daemon mode and into the codesearch helper.
//
// indexing.Manager deliberately runs the google/codesearch library in a child
// process: upstream calls fatal exits on a corrupt index and leaks mmapped
// regions, neither of which may be allowed to take down a long-lived daemon.
// The child it spawns is this binary, so this name is a contract with
// internal/indexing -- renaming either side silently disables indexed search
// and falls the agent back to unindexed grep.
const codesearchHelperCommand = "workspace-codesearch"

// IsCodesearchHelperArgs reports whether args (os.Args[1:]) selects the
// helper rather than the daemon.
func IsCodesearchHelperArgs(args []string) bool {
	return len(args) > 0 && args[0] == codesearchHelperCommand
}

// RunCodesearchHelper executes one helper subcommand and returns a process
// exit code. Output is the machine-readable result the parent decodes; all
// diagnostics go to stderr so they cannot corrupt it.
func RunCodesearchHelper(args []string, out io.Writer) int {
	if len(args) == 0 {
		helperErrorf("%s: expected a subcommand (build|candidates)\n", codesearchHelperCommand)
		return 2
	}

	ctx := context.Background()
	switch args[0] {
	case "build":
		fs := newHelperFlagSet("build")
		root := fs.String("root", "", "workspace root to index")
		index := fs.String("index", "", "path of the index sidecar to write")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *root == "" || *index == "" {
			helperErrorf("build: --root and --index are required\n")
			return 2
		}
		if err := indexing.BuildIndex(ctx, *root, *index); err != nil {
			helperErrorf("build: %v\n", err)
			return 1
		}
		return 0

	case "candidates":
		return runCandidates(args[1:], out)

	default:
		helperErrorf("%s: unknown subcommand %q\n", codesearchHelperCommand, args[0])
		return 2
	}
}

func newHelperFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(flagErrorOutput)
	return fs
}

// runCandidates handles the "candidates" subcommand, returning candidate
// file paths as a JSON array on stdout.
func runCandidates(args []string, out io.Writer) int {
	fs := newHelperFlagSet("candidates")
	index := fs.String("index", "", "path of the index sidecar to query")
	pattern := fs.String("pattern", "", "search pattern")
	literal := fs.Bool("literal", false, "treat the pattern as a literal string")
	caseSensitive := fs.Bool("case-sensitive", false, "match case sensitively")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *index == "" || *pattern == "" {
		helperErrorf("candidates: --index and --pattern are required\n")
		return 2
	}
	files, err := indexing.IndexCandidates(*index, *pattern, *literal, *caseSensitive)
	if err != nil {
		helperErrorf("candidates: %v\n", err)
		return 1
	}
	// The parent decodes a JSON array and treats any decode failure as
	// "no index", so a nil slice must still marshal as [] rather than null.
	if files == nil {
		files = []string{}
	}
	encoded, err := json.Marshal(files)
	if err != nil {
		helperErrorf("candidates: encode result: %v\n", err)
		return 1
	}
	if _, err := out.Write(encoded); err != nil {
		helperErrorf("candidates: write result: %v\n", err)
		return 1
	}
	return 0
}

// helperErrorf writes a diagnostic to stderr. The write result is ignored
// deliberately: if stderr is gone there is nowhere left to report it, and the
// exit code already carries the outcome the parent acts on.
func helperErrorf(format string, args ...any) {
	_, _ = fmt.Fprintf(flagErrorOutput, format, args...)
}
