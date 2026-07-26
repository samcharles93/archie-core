// Package channels defines the interface every chat channel must satisfy
// to integrate with archie-core's gateway router. Channels own their
// persistent connection lifecycle; the gateway handles message dispatch.
//
// Each channel declares its required configuration via ConfigSchema so
// the daemon can validate wiring at startup — catching missing tokens,
// invalid ports, or misconfigured credentials before accepting connections.
package channels

import (
	"encoding/json"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// Channel is the contract every chat platform must satisfy. It extends
// gateway.Gateway with configuration introspection so the daemon can
// validate and introspect channels at startup without knowing about
// specific implementations.
type Channel interface {
	gateway.Gateway

	// ConfigSchema returns the JSON Schema for this channel's required
	// configuration. The daemon uses this at startup to validate that
	// the user's config file provides all required fields and to surface
	// clear errors when a channel is misconfigured.
	//
	// The schema must include:
	//   - type: object
	//   - required: [...]  (fields that must be present)
	//   - properties: {...} (field definitions with types and descriptions)
	ConfigSchema() json.RawMessage

	// ValidateConfig checks that the provided configuration is valid for
	// this channel. Returns nil when the config is acceptable, or a
	// descriptive error when required fields are missing or invalid.
	//
	// The cfg argument is the channel-specific portion of the daemon
	// config (e.g. the [chat.telegram] block, not the full Config).
	ValidateConfig(cfg map[string]any) error
}
