package nats

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
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
)

// Config describes how to reach NATS and how the stream and consumer should
// behave. The zero value is not usable; call [Config.withDefaults] via
// [Connect], which fills every unset field.
type Config struct {
	// URL is the NATS server address. Required.
	URL string

	// Token authenticates the connection. Empty means no token auth.
	Token string

	// StreamName is the JetStream stream holding the bound subjects.
	StreamName string

	// Subjects are the subjects the stream carries. Required.
	//
	// Composition supplies these rather than the package hardcoding them: the
	// bus must not know which subjects belong to which domain, and archied
	// and archie-agent previously declared the same stream separately, free
	// to disagree about its subjects, retention and dedup window.
	Subjects []string

	// ConsumerName is the durable pull consumer Fetch reads from.
	ConsumerName string

	// FilterSubject restricts that consumer to a subset of the stream --
	// archied consumes task subjects, archie-agent consumes agent subjects.
	// Required.
	FilterSubject string

	// Retention is the stream's retention policy. Nil defaults to
	// jetstream.WorkQueuePolicy (each message claimed by exactly one
	// consumer), which is correct for ARCHIE_TASKS task distribution. A
	// fan-out reaction stream passes jetstream.LimitsPolicy so every
	// registered consumer sees every matching event; under WorkQueuePolicy a
	// second consumer on an overlapping filter subject silently receives
	// nothing (the trap documented in CLAUDE.md). The pointer form is what
	// lets "unset" default to WorkQueuePolicy when the policy's zero value
	// (LimitsPolicy) is a valid explicit choice.
	Retention *jetstream.RetentionPolicy

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
	if len(c.Subjects) == 0 {
		return fmt.Errorf("%w: Subjects is required", ErrInvalidConfig)
	}
	if c.FilterSubject == "" {
		return fmt.Errorf("%w: FilterSubject is required", ErrInvalidConfig)
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
	if c.Retention == nil {
		policy := jetstream.WorkQueuePolicy
		c.Retention = &policy
	}
	return c
}
