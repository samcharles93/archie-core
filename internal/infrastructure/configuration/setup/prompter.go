// Package setup builds a config.Config and the TOML edits to persist it,
// driven by archied's interactive first-run configuration flow. It has no
// terminal dependency of its own: everything it needs from a user-facing
// prompt surface is the [Prompter] interface below, so the step logic is
// testable end to end without a TTY.
package setup

import "context"

// Prompter is what setup's step logic needs from a prompt surface. The real
// implementation talks to a terminal (internal/infrastructure/
// terminalprompt); tests drive steps through a fake.
//
// Every method takes a context so a step can be cancelled mid-prompt (e.g.
// Ctrl+C) rather than leaving the operator stuck at a prompt an outer
// timeout or signal has already decided to abandon.
type Prompter interface {
	// Select presents options and returns the chosen index. Returns -1 and
	// ctx.Err() if ctx is done before an answer is given.
	Select(ctx context.Context, prompt string, options []string) (int, error)
	// ReadLine reads one line of visible input. An empty answer returns
	// defaultValue.
	ReadLine(ctx context.Context, prompt, defaultValue string) (string, error)
	// ReadSecret reads one line of input without echoing it.
	ReadSecret(ctx context.Context, prompt string) (string, error)
	// Confirm asks a yes/no question. An empty answer returns defaultYes.
	Confirm(ctx context.Context, prompt string, defaultYes bool) (bool, error)
}
