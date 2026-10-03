// Package installtype reports how the binary was distributed, stamped at
// build time. Unstamped builds report Unknown.
package installtype

// Unknown is what Type reports when a build was never stamped.
const Unknown = "unknown"

// buildType is stamped per release artifact via
// "-ldflags -X github.com/samcharles93/archie-core/internal/installtype.buildType=<value>",
// one value per build target -- e.g. "binary" for a native build,
// "container" for a Docker image build. Never set anywhere else.
var buildType = Unknown

// Type reports how this binary was built for distribution: Unknown unless
// stamped by the release pipeline.
func Type() string {
	if buildType == "" {
		return Unknown
	}
	return buildType
}
