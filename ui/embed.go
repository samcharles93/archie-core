//go:build !no_ui

// Package ui embeds the built dashboard in dist/, which is committed. Rebuild
// with `task ui`.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distDir embed.FS

// DistDirFS is the built dashboard rooted at dist/, or nil when the binary
// was built with the no_ui tag.
var DistDirFS, _ = fs.Sub(distDir, "dist")
