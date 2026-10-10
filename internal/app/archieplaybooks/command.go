package archieplaybooks

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Run runs `archied playbooks <command>`: lint for CI, serve for editors.
// Both use the loaders the daemon runs, so a check here cannot disagree with
// what the daemon accepts at startup.
func Run(args []string, stderr io.Writer) int {
	commands := map[string]func([]string, io.Writer) int{
		"lint":  runLint,
		"serve": runServe,
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: archied playbooks <command> [args]")
		fmt.Fprintln(stderr, "commands:")
		for name := range commands {
			fmt.Fprintf(stderr, "  %s\n", name)
		}
		return 2
	}

	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "archied playbooks: unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:], stderr)
}

// runLint validates an EDA playbook directory against the compiler the daemon
// runs. Exit codes: 0 clean, 1 findings, 2 usage error.
func runLint(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("archied playbooks lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	edaDir := flags.String("eda-dir", "", "EDA playbook directory to lint")
	if err := flags.Parse(args); err != nil {
		return 2 // flag.ContinueOnError already printed the message
	}
	if *edaDir == "" {
		fmt.Fprintln(stderr, "lint: -eda-dir is required")
		flags.Usage()
		return 2
	}
	return LintEDA(*edaDir, stderr).ExitCode
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
	if err := Serve(ctx, stdio{}); err != nil {
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
