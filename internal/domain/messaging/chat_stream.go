package messaging

// ChatSnapshot is what Snapshot returns: the full chat state a dashboard
// renders in one read.
type ChatSnapshot struct {
	Sessions              []SessionContext
	Models                []string
	ModelsByProvider      map[string][]string
	Providers             []string
	ActiveModel           string
	ActiveProvider        string
	Personas              []string
	ActivePersonas        map[string]string
	RestartAvailable      bool
	CancellationAvailable bool
	PersonasAvailable     bool
}

// ChatReply is the blocking reply from Route.
type ChatReply struct {
	Text      string
	SessionID string
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
