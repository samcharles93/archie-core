package setup

import "context"

// Prompter is the prompt surface setup asks questions through. Every method
// takes a context so a prompt can be cancelled.
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
