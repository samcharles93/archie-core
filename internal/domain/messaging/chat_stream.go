package messaging

// ChatSnapshot is what Snapshot returns: the full chat state a dashboard
// renders in one read.
type ChatSnapshot struct {
	Sessions         []SessionContext
	Models           []string
	ModelsByProvider map[string][]string
	Providers        []string
	ActiveModel      string
	ActiveProvider   string
	Personas         []string
	ActivePersonas   map[string]string
	// Version is the Gateway's rendered component-version block. Channel
	// frontends display it rather than stamping their own build, so a
	// partially upgraded deployment cannot report itself as matched.
	Version               string
	RestartAvailable      bool
	CancellationAvailable bool
	PersonasAvailable     bool
}

// ChatReply is the blocking reply from Route.
type ChatReply struct {
	Text      string
	SessionID string
	// RateLimited reports whether Text is the inbound rate-limit reply rather
	// than a real dispatch outcome. A human-facing channel can ignore it and
	// send Text as usual; a channel with no human reading replies (a webhook)
	// must not treat it as a normal reply or a delivered event -- see
	// archie-core-1173.
	RateLimited bool
}

// ChatCancellation is what Cancel returns.
type ChatCancellation struct {
	Cancelled bool
	Dropped   int
}

// ChatEvent contains values only; Text holds the error message for error events.
type ChatEvent struct {
	Kind      string
	Text      string
	SessionID string
	Tool      ToolCallEvent
	Media     MediaEvent
}

// DashboardNavigateResult is what dashboard_navigate returns. The web UI
// renders it as a clickable chip that routes the operator to the page.
type DashboardNavigateResult struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}
