//go:build !no_ui

// Package ui embeds archied's built dashboard. It carries no behaviour --
// internal/webui owns serving and the HTTP API -- so that the frontend build
// artefact stays a separate concern from the server that hands it out.
//
// dist/ is committed deliberately. Without it `go build`, `go install` and
// the CI gate would each need a Node toolchain present merely to compile the
// daemon, which is a poor trade for a project whose quality gate is
// `task check`. Rebuild it with `task ui`.
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
