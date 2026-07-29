package configuration

import "errors"

// Sentinel errors callers can match with errors.Is. Everything else this
// package returns is wrapped with the offending path via %w.
//
// The distinction that matters to a caller is whether the operator can fix
// the problem by editing a file (ErrInvalidInput) or whether the
// configuration could not be read at all (ErrNoConfigFile, ErrUnreadable).
// The previous code signalled all of these with fmt.Errorf strings prefixed
// "config: ", so a caller had to match on message text to tell them apart.
var (
	// ErrInvalidInput reports configuration that decoded successfully but
	// describes something unusable -- an unknown enum value, a missing
	// required field, a malformed URL.
	ErrInvalidInput = errors.New("configuration: invalid input")

	// ErrNoConfigFile reports a directory containing neither config.yaml
	// nor config.toml where one was required.
	ErrNoConfigFile = errors.New("configuration: no config file")

	// ErrUnknownFeature reports a config.<name>.yaml whose <name> is not a
	// recognised feature. Ignoring these silently would hide a typo as a
	// disabled feature.
	ErrUnknownFeature = errors.New("configuration: unrecognized feature file")

	// ErrDuplicateFeature reports two files claiming the same feature, where
	// choosing either would apply settings the operator cannot predict from
	// the filenames.
	ErrDuplicateFeature = errors.New("configuration: duplicate feature file")

	// ErrUnreadable reports a file or directory that could not be read or
	// decoded.
	ErrUnreadable = errors.New("configuration: unreadable")
)
