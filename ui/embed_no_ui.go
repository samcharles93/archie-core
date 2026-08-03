//go:build no_ui

package ui

import "io/fs"

// DistDirFS is deliberately nil under the no_ui build tag so the dashboard is
// not bundled. archie-agent has no use for it, and leaving it out keeps the
// per-task container image smaller. internal/webui serves an explanatory page
// rather than failing when this is nil.
var DistDirFS fs.FS
