package setup

import "context"

// DefaultPrompter answers every question with the prompt's own default and
// never touches a terminal. archied setup uses it for --defaults, so an
// unattended install resolves every question the baseline Params do not
// cover without prompting: ReadLine returns defaultValue, Select returns the
// first option, Confirm returns defaultYes, and ReadSecret returns "" (a
// secret has no default, and the steps treat a blank secret as "leave blank
// to configure later").
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
