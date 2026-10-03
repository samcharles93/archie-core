package config

// ReloadStatus is the outcome of the last config reload, shown on the
// dashboard.
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
