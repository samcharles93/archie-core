package setup

import "context"

// DefaultPrompter answers every prompt with its default without a terminal:
// the default value, the first option, defaultYes, and "" for secrets.
type DefaultPrompter struct{}

// Select returns the first option.
func (DefaultPrompter) Select(context.Context, string, []string) (int, error) { return 0, nil }

// ReadLine returns defaultValue.
func (DefaultPrompter) ReadLine(_ context.Context, _, defaultValue string) (string, error) {
	return defaultValue, nil
}

// ReadSecret returns the empty string.
func (DefaultPrompter) ReadSecret(context.Context, string) (string, error) { return "", nil }

// Confirm returns defaultYes.
func (DefaultPrompter) Confirm(_ context.Context, _ string, defaultYes bool) (bool, error) {
	return defaultYes, nil
}
