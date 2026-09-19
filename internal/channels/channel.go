// Package channels defines the interface every chat channel must satisfy
// to integrate with archie-core's messaging layer. Channels own their
// persistent connection lifecycle; the Gateway handles message dispatch.
package channels

import (
	"context"
	"encoding/json"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// Lifecycle receives adapter-owned startup facts.
type Lifecycle = messaging.Lifecycle

// Channel is the contract every chat platform must satisfy.
type Channel interface {
	Name() string
	Start(ctx context.Context, client messaging.ChatContract, lifecycle Lifecycle) error
	Stop(ctx context.Context) error

	// ConfigSchema returns the JSON Schema for this channel's required configuration.
	ConfigSchema() json.RawMessage

	// ValidateConfig checks that the provided configuration is valid for this channel.
	ValidateConfig(cfg map[string]any) error
}
