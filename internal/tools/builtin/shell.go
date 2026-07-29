// Lifted from tau internal/agent/tools/shell.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//   - the command runs in its own process group and cancellation signals
//     the whole group. Upstream relies on exec.CommandContext's default,
//     which kills only the direct shell, so `go test ./...` left its test
//     binary running and `nohup x &` survived outright. archie's /stop
//     cancels the turn context, so without this the command it was aimed
//     at would keep going. tau applies the same treatment to spawned
//     agents in agent.go; this extends it to the shell tool.
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	defaultShellTimeout = 120 * time.Second
	maxShellTimeout     = 10 * time.Minute

	// shellWaitDelay bounds how long Wait keeps reading from inherited
	// pipes after the process group has been signalled.
	shellWaitDelay = 2 * time.Second
)

// ShellParams are the parameters for the shell tool.
type ShellParams struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds, defaults to 120
}

var shellSchema = Schema{
	Name:        "shell",
	Description: fmt.Sprintf("Execute a shell command. Uses PowerShell on Windows, bash on Linux/macOS. Returns stdout and stderr, truncated to the last %d lines or %s (whichever is hit first); when truncated, the full output is saved to a temp file whose path is included in the notice. Use for builds, tests, git, and other commands - prefer the dedicated grep, find, and read tools for searching and reading files.", DefaultMaxLines, FormatSize(DefaultMaxBytes)),
	Parameters: json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds. Defaults to 120."
			}
		},
		"required": ["command"]
	}`),
}

// NewShellTool creates the built-in shell execution tool.
func NewShellTool(cwd string, mq *MutationQueue) Tool {
	return Tool{
		Schema:  shellSchema,
		Source:  "builtin",
		Execute: makeShellExecutor(cwd, mq),
	}
}

type bridgeWriter struct {
	bridge UIBridge
}

func (w *bridgeWriter) Write(p []byte) (n int, err error) {
	w.bridge.Log(string(p))
	return len(p), nil
}

func makeShellExecutor(cwd string, mq *MutationQueue) Executor {
	return func(ctx context.Context, params json.RawMessage, bridge UIBridge) (Result, error) {
		var p ShellParams
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{Content: fmt.Sprintf("invalid parameters: %v", err), IsError: true}, nil
		}

		p.Command = strings.TrimSpace(p.Command)
		if p.Command == "" {
			return Result{Content: "command is required", IsError: true}, nil
		}

		timeout := defaultShellTimeout
		if p.Timeout > 0 {
			timeout = time.Duration(p.Timeout) * time.Second
		}
		if timeout > maxShellTimeout {
			timeout = maxShellTimeout
		}

		if cwd != "" {
			info, err := os.Stat(cwd)
			if err != nil || !info.IsDir() {
				return Result{Content: fmt.Sprintf("invalid cwd %q", cwd), IsError: true}, nil
			}
		}

		// Serialize with file-mutation tools: shell commands may modify
		// files that write/edit are working on.
		if mq != nil {
			mq.GlobalLock()
			defer mq.GlobalUnlock()
		}

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		shell, args := shellCommand(p.Command)
		cmd := exec.CommandContext(ctx, shell, args...)
		if cwd != "" {
			cmd.Dir = cwd
		}

		// Run the command as its own process group and, on cancellation,
		// signal the group rather than the shell alone.
		//
		// exec.CommandContext's default kills only the process it started.
		// A shell's real work is almost always a child of that shell, so
		// the default leaves the actual command running: cancelling
		// `go test ./...` reaps the shell and lets the test binary carry
		// on, and anything backgrounded with & or nohup is never touched.
		// /stop cancels this context, so that gap is the difference
		// between stopping a command and only appearing to.
		setProcessGroup(cmd)
		cmd.Cancel = func() error { return signalProcessGroup(cmd, syscall.SIGKILL) }
		// Stop waiting on inherited pipes shortly after the kill. A
		// grandchild holding stdout open would otherwise keep Wait
		// blocked long after the group was signalled.
		cmd.WaitDelay = shellWaitDelay

		var stdout, stderr bytes.Buffer
		bw := &bridgeWriter{bridge: bridge}
		cmd.Stdout = io.MultiWriter(&stdout, bw)
		cmd.Stderr = io.MultiWriter(&stderr, bw)

		err := cmd.Run()

		var b strings.Builder
		if stdout.Len() > 0 {
			b.WriteString(stdout.String())
		}
		if stderr.Len() > 0 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("[stderr]\n")
			b.WriteString(stderr.String())
		}

		output := b.String()
		if output == "" {
			output = "(no output)"
		}

		// Collapse before truncation, so a long passing test run is reduced to
		// its summaries rather than tail-truncated into a partial log.
		output, _ = collapseGoTestOutput(p.Command, output, err == nil)

		tr := TruncateTail(output, DefaultMaxLines, DefaultMaxBytes)
		content := tr.Content
		if tr.Truncated {
			// Save the full output so the model can grep/tail it instead of
			// re-running an expensive command.
			if path, saveErr := saveFullOutput(output); saveErr == nil {
				content += "\n[full output saved to: " + path + "]"
			}
		}

		if err != nil {
			// Check context cancellation first - a process may exit with
			// a non-zero code after the deadline, and we want to report
			// timeout rather than a misleading exit code.
			if ctx.Err() != nil {
				return Result{
					Content: fmt.Sprintf("[timeout after: %s]\n%s", timeout, content), IsError: true,
					ErrorKind: "timeout", Truncated: tr.Truncated, ResultBytes: len(output),
				}, nil
			}

			if exitErr, ok := err.(*exec.ExitError); ok {
				return Result{
					Content: fmt.Sprintf("[exit code: %d]\n%s", exitErr.ExitCode(), content), IsError: true,
					ErrorKind: "command_exit", Truncated: tr.Truncated, ResultBytes: len(output),
				}, nil
			}

			return Result{Content: fmt.Sprintf("error executing command: %v", err), IsError: true}, nil
		}

		return Result{Content: content, Truncated: tr.Truncated, ResultBytes: len(output)}, nil
	}
}

// goTestSummaryRe matches cmd/go's per-package result lines: "ok", "FAIL" and
// "?" (no test files). Note it must NOT match "---", which prefixes per-test
// result lines ("--- PASS: TestX") - exactly the noise being collapsed. A
// package that fails to build reports as "FAIL <pkg> [build failed]", so the
// package-level failure case is already covered by the FAIL alternative.
var goTestSummaryRe = regexp.MustCompile(`^(ok|FAIL|\?)\s`)

// goTestInvocationRe matches a `go test` invocation, allowing for a leading
// `cd ... &&` and for flags between `go` and `test`. It deliberately does not
// match wrappers like gotestsum, whose output format differs.
var goTestInvocationRe = regexp.MustCompile(`(^|[;&|]\s*)go\s+test\b`)

// collapseGoTestOutput reduces a PASSING `go test` run to its per-package
// summary lines. A single `go test ./internal/tui2/... -v` in the analysed
// sessions took 14.2s and returned 11,044 tokens - 11% of all shell result
// tokens in one call - despite every test having passed, where the only
// information the model needed was "they passed".
//
// Failing runs are returned verbatim: on failure the per-test detail is the
// entire point, and collapsing it would force a re-run to recover it.
func collapseGoTestOutput(command, output string, succeeded bool) (string, bool) {
	if !succeeded || !goTestInvocationRe.MatchString(command) {
		return output, false
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if goTestSummaryRe.MatchString(l) {
			kept = append(kept, l)
		}
	}

	// Nothing recognisable to keep, or nothing to gain: leave it alone rather
	// than returning an empty result or appending a pointless notice.
	if len(kept) == 0 || len(kept) == len(lines) {
		return output, false
	}

	return fmt.Sprintf(
		"%s\n\n[%d lines of passing per-test output collapsed; re-run with -v and a narrower package to see it]",
		strings.Join(kept, "\n"), len(lines)-len(kept),
	), true
}

// saveFullOutput writes the complete, untruncated command output to a temp
// file and returns its path.
func saveFullOutput(output string) (string, error) {
	f, err := os.CreateTemp("", "tau-shell-*.log")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(output); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// shellCommand returns the shell binary and arguments for the current platform.
func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		// Prefer PowerShell 7 (pwsh), fall back to Windows PowerShell.
		shell := "powershell.exe"
		if _, err := exec.LookPath("pwsh"); err == nil {
			shell = "pwsh"
		}
		return shell, []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "Bypass",
			"-Command", command,
		}
	}
	return "bash", []string{"-c", command}
}
