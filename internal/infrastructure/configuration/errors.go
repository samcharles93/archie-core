package configuration

import "errors"

// Sentinel errors for errors.Is. Other errors are wrapped with the path.
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
