// Command archie-playbooks is the standalone playbook tool for archie-core,
// shaped like gopls: one binary, multiple invocation modes. It validates
// playbook YAML binding files against the same schema the daemon loads at
// startup, so a pre-merge check can never disagree with runtime validation.
//
// Today it ships one mode (lint). The command dispatch is a table from day
// one, so a future serve/lsp mode slots in without restructuring -- both
// modes are entrypoints into the same validation source in
// internal/domain/workflow.
//
// Known limitation (as of this slice): findings are file-granular, not
// line-granular. The loader decodes playbooks with yaml.Unmarshal into a
// plain map, which discards line numbers; a compiler-style file:line
// diagnostic needs a yaml.Node decoding upgrade, which is out of scope
// here and tracked separately.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/samcharles93/archie-core/internal/app/archieplaybooks"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	// Subcommand table from day one (gopls shape): each mode owns its flag
	// set and returns an exit code. Adding serve/lsp later is a new entry
	// here, not a restructuring.
	commands := map[string]func([]string, io.Writer) int{
		"lint": runLint,
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: archie-playbooks <command> [args]")
		fmt.Fprintln(stderr, "commands:")
		for name := range commands {
			fmt.Fprintf(stderr, "  %s\n", name)
		}
		return 2
	}

	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "archie-playbooks: unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:], stderr)
}

// runLint validates one or more playbook directories against the domain
// loaders and reports collisions / malformed files / invalid bindings.
// Exit codes: 0 clean, 1 findings, 2 usage error.
func runLint(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("archie-playbooks lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dirs multiFlag
	flags.Var(&dirs, "dir", "playbook directory to lint (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 2 // flag.ContinueOnError already printed the message
	}
	if len(dirs) == 0 {
		fmt.Fprintln(stderr, "lint: at least one -dir is required")
		flags.Usage()
		return 2
	}

	result := archieplaybooks.Lint(dirs, stderr)
	return result.ExitCode
}

// multiFlag collects repeated -dir flags.
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprint([]string(*m)) }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
