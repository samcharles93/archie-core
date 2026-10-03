package configtemplate

import _ "embed"

// Changelog is the verbatim contents of CHANGELOG.md, so a binary carries the
// release notes for the version it was built as.
//
//go:embed CHANGELOG.md
var Changelog string
