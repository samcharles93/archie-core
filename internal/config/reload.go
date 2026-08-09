package config

// ReloadStatus is the outcome of the most recent config reload attempt,
// surfaced so the dashboard can tell the operator when the running
// config is stale: a reload that failed validation leaves the daemon
// running on the previous config, and that failure must be visible in
// the UI, not buried in logs.
type ReloadStatus struct {
	// LastError is the validation error from the most recent failed
	// reload attempt. Empty when the most recent attempt succeeded.
	LastError string `json:"last_error,omitempty"`
	// LastErrorAt is the RFC3339 UTC timestamp of the failed attempt.
	LastErrorAt string `json:"last_error_at,omitempty"`
	// LastReloadAt is the RFC3339 UTC timestamp of the most recent
	// successful reload. Empty until the first reload.
	LastReloadAt string `json:"last_reload_at,omitempty"`
}
