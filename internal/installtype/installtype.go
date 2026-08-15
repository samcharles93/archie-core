// Package installtype identifies how the running binary was distributed, so
// code that must never guess at deployment shape -- most notably the
// self-update dispatcher -- can fail closed instead of assuming.
//
// The install type is decided once, at release-build time, and baked into
// the binary. It is deliberately never detected at runtime by probing the
// environment (checking for a container marker file, a systemd env var,
// and so on): those signals are guessable and can be present for reasons
// that have nothing to do with how the binary would actually be updated. A
// binary that was never stamped by the release pipeline (a local
// `go build`, for instance) reports Unknown, and callers that would
// otherwise silently guess must instead refuse.
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
