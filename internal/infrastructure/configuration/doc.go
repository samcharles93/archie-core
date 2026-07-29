// Package configuration is the external configuration input boundary.
//
// It owns everything about how configuration arrives: locating files,
// decoding TOML and YAML, applying base/overlay precedence, resolving
// environment and secret references, defaulting absent input, validating
// what was supplied, and recording where each value came from.
//
// It owns none of the behaviour those values describe. Deciding what a
// dispatch trigger or a retry budget *means* belongs to the domain that
// acts on it; this package only decides whether the operator supplied
// something usable, and says so clearly when they did not.
//
// # Why this package exists
//
// Loading used to live inside internal/config, alongside the runtime types
// every package imports. That made the shared model responsible for file
// I/O, so a package that merely wanted a settings struct also pulled in a
// TOML decoder, and defaulting, validation and decoding were interleaved in
// one function. See docs/architecture/configuration.md.
//
// # The temporary seam
//
// docs/architecture/configuration.md requires a private input document that
// is translated into per-domain settings and never returned as the
// application's runtime model. [Document.Config] is not that yet: it is the
// existing internal/config.Config, exposed here so this package can take
// over loading without every consumer changing at once.
//
// It is deliberately a named field rather than a bare return value so the
// compromise is visible at each use site. It should be replaced by typed
// per-domain sections once those domains own their settings; the condition
// for deleting it is that internal/config no longer exists. Until then, do
// not add behaviour to it, and do not treat its presence as licence to pass
// whole-application configuration into a domain.
package configuration
