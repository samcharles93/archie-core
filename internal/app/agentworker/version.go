package agentworker

// version is set at build time with -ldflags -X and reported to the daemon.
var version = "dev"

// Version reports the archie-agent build this worker process was built
// from, or "dev" for an unstamped build (a local `go build`, for instance).
func Version() string {
	if version == "" {
		return "dev"
	}
	return version
}
