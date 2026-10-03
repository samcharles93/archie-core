// Package log defines the "log" module kind's Args and Result.
package log

// SchemaVersion is independent of a playbook's own workflow_version, per
// eda-playbook-engine.md's schema-versioning point.
const SchemaVersion = 1

// Args is the accepted message shape for the log kind. Invoke decodes the
// playbook's args map into this struct before calling the interpreted
// function; a shape mismatch is a reported failure, not a silent zero fill.
type Args struct {
	// Message is the line to write to the daemon's log. Required.
	Message string
	// Level is optional; the implementation may default it.
	Level string
}

// Result is what the log kind returns. It is surfaced as map[string]any so a
// later data-flow mechanism can use it (module-position.md open question 3).
type Result struct {
	// Written reports whether the implementation wrote the line.
	Written bool
	// Level echoes the level actually used.
	Level string
}
