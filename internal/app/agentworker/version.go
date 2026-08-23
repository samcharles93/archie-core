package agentworker

// version is stamped per release artifact via
// "-ldflags -X github.com/samcharles93/archie-core/internal/app/agentworker.version=<value>",
// the same mechanism internal/installtype uses for buildType -- see that
// package's doc comment for why this is baked in at build time rather than
// detected. Reported back to the daemon in every taskrun.Response
// (see task_execution.go) so it can observe the build actually running,
// not the one its own release pipeline expected.
var version = "dev"

// Version reports the archie-agent build this worker process was built
// from, or "dev" for an unstamped build (a local `go build`, for instance).
func Version() string {
	if version == "" {
		return "dev"
	}
	return version
}
