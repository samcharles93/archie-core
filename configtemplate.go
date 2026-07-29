// Package configtemplate embeds config.example.toml so archied setup can
// generate a first-run config.toml from the exact file operators already
// read as documentation, instead of a second copy that can drift from it.
//
// It lives at the module root, rather than under internal/, because
// go:embed patterns cannot reach outside the directory of the .go file
// that declares them, and config.example.toml is kept at the root where
// it is easy to find and hand-edit.
package configtemplate

import _ "embed"

// Example is the verbatim contents of config.example.toml.
//
//go:embed config.example.toml
var Example []byte
