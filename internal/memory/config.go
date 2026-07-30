package memory

// ── Configuration helper interfaces ───────────────────────────────────────
//
// Providers that support dynamic configuration implement these interfaces.
// The MemoryManager uses ConfigSchemaProvider to validate user-provided
// config before passing it to SaveConfigProvider for persistence.

// ConfigSchemaProvider is an optional interface for providers that expose a
// JSON Schema describing their configuration shape. The schema is used to
// validate user-provided config values before they are persisted.
type ConfigSchemaProvider interface {
	// GetConfigSchema returns a JSON Schema document (as map[string]any)
	// describing the provider's configuration. The schema is used by the
	// config system to validate user-provided values.
	GetConfigSchema() map[string]any
}

// SaveConfigProvider is an optional interface for providers that support
// persisting configuration. The MemoryManager calls SaveConfig after
// validating user-provided values against the schema (if the provider
// also implements ConfigSchemaProvider).
type SaveConfigProvider interface {
	// SaveConfig validates and persists the given configuration values.
	// values is the complete set of provider-specific config key/value
	// pairs. dataHome is the root of the data directory tree; the provider
	// should write its config to a provider-specific location under this
	// path.
	SaveConfig(values map[string]any, dataHome string) error
}
