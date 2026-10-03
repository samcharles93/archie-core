// Package configtemplate embeds config.example.toml for archied setup. It
// sits at the module root because embed cannot reach parent directories.
package configtemplate

import _ "embed"

// Example is the verbatim contents of config.example.toml.
//
//go:embed config.example.toml
var Example []byte
