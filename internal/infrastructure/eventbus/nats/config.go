package nats

import (
	"fmt"
	"time"
)

// Defaults for Config. Previously these were unexported package constants,
// which made stream naming and timeouts untunable per deployment.
const (
	DefaultStreamName   = "ARCHIE_TASKS"
	DefaultConsumerName = "archie-daemon"
	DefaultDedupWindow  = 2 * time.Minute
	DefaultPollTimeout  = 2 * time.Second
	DefaultAckWait      = 5 * time.Minute
	DefaultMaxDeliver   = 3
	DefaultInactiveTTL  = 24 * time.Hour

	// dedupKeyPrefix namespaces the Nats-Msg-Id used for JetStream dedup so
	// archie's keys cannot collide with another publisher on a shared server.
	dedupKeyPrefix = "archie:"
)

// Config describes how to reach NATS and how the stream and consumer should
// behave. The zero value is not usable; call [Config.withDefaults] via
// [Connect], which fills every unset field.
type Config struct {
	// URL is the NATS server address. Required.
	URL string

	// Token authenticates the connection. Empty means no token auth.
	Token string

	// StreamName is the JetStream stream holding task and agent subjects.
	StreamName string

	// ConsumerName is the durable pull consumer the daemon reads tasks from.
	ConsumerName string

	// DedupWindow is how long JetStream remembers a Nats-Msg-Id, suppressing
	// republished duplicates of the same issue within the window.
	DedupWindow time.Duration

	// PollTimeout bounds a single Fetch. On expiry Fetch reports ErrNoMessage.
	PollTimeout time.Duration

	// AckWait is how long a delivered message may go unacknowledged before
	// JetStream redelivers it. It must exceed the longest stage runtime.
	AckWait time.Duration

	// MaxDeliver caps redelivery attempts before a message is dropped.
	MaxDeliver int

	// InactiveTTL removes the consumer after this much inactivity.
	InactiveTTL time.Duration
}

// Validate reports whether the config can produce a usable client.
func (c Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("%w: URL is required", ErrInvalidConfig)
	}
	if c.PollTimeout < 0 || c.AckWait < 0 || c.DedupWindow < 0 {
		return fmt.Errorf("%w: durations must not be negative", ErrInvalidConfig)
	}
	if c.MaxDeliver < 0 {
		return fmt.Errorf("%w: MaxDeliver must not be negative", ErrInvalidConfig)
	}
	return nil
}

// withDefaults returns a copy with every unset field populated, so the rest of
// the package never has to test for zero values.
func (c Config) withDefaults() Config {
	if c.StreamName == "" {
		c.StreamName = DefaultStreamName
	}
	if c.ConsumerName == "" {
		c.ConsumerName = DefaultConsumerName
	}
	if c.DedupWindow == 0 {
		c.DedupWindow = DefaultDedupWindow
	}
	if c.PollTimeout == 0 {
		c.PollTimeout = DefaultPollTimeout
	}
	if c.AckWait == 0 {
		c.AckWait = DefaultAckWait
	}
	if c.MaxDeliver == 0 {
		c.MaxDeliver = DefaultMaxDeliver
	}
	if c.InactiveTTL == 0 {
		c.InactiveTTL = DefaultInactiveTTL
	}
	return c
}
