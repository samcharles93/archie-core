// Command archie-playbooks is the standalone playbook tool for archie-core,
// shaped like gopls: one binary, multiple invocation modes. It validates
// playbook YAML binding files against the same schema the daemon loads at
// startup, so a pre-merge check can never disagree with runtime validation.
//
// It ships two modes: lint for CI, and serve, a language server that
// publishes the same findings as editor diagnostics. Both call the loaders
// the daemon runs.
//
// A finding about one binding key leads with the key's file:line; a file
// that does not parse is reported by path with the YAML parser's message.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/samcharles93/archie-core/internal/app/archieplaybooks"
	"github.com/samcharles93/archie-core/internal/buildinfo"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	// Subcommand table from day one (gopls shape): each mode owns its flag
	// set and returns an exit code. Adding serve/lsp later is a new entry
	// here, not a restructuring.
	commands := map[string]func([]string, io.Writer) int{
		"lint":  runLint,
		"serve": runServe,
	}

	if len(args) > 0 && (args[0] == "-version" || args[0] == "--version") {
		buildinfo.Print("archie-playbooks")
		return 0
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

// runLint validates routing binding directories (-dir) and an EDA playbook
// directory (-eda-dir) against the loaders the daemon runs at startup.
// Exit codes: 0 clean, 1 findings, 2 usage error.
func runLint(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("archie-playbooks lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dirs multiFlag
	flags.Var(&dirs, "dir", "routing binding directory to lint (repeatable)")
	edaDir := flags.String("eda-dir", "", "EDA playbook directory to lint (the daemon's eda_playbook_dir)")
	if err := flags.Parse(args); err != nil {
		return 2 // flag.ContinueOnError already printed the message
	}
	if len(dirs) == 0 && *edaDir == "" {
		fmt.Fprintln(stderr, "lint: at least one -dir or an -eda-dir is required")
		flags.Usage()
		return 2
	}

	code := 0
	if len(dirs) > 0 {
		code = max(code, archieplaybooks.Lint(dirs, stderr).ExitCode)
	}
	if *edaDir != "" {
		code = max(code, archieplaybooks.LintEDA(*edaDir, stderr).ExitCode)
	}
	return code
}

// runServe runs the language server on stdin/stdout until the editor
// disconnects or the process is signalled.
func runServe(args []string, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "serve: takes no arguments; the editor speaks LSP on stdin/stdout")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := archieplaybooks.Serve(ctx, stdio{}); err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	return 0
}

// stdio joins stdin and stdout into the one stream the language server reads
// and writes.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }

// multiFlag collects repeated -dir flags.
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprint([]string(*m)) }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
