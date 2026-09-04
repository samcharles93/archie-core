package nats

import (
	"fmt"
	"time"
)

// Defaults for Config. Previously these would have been unexported package
// constants, which made bucket naming and timeouts untunable per deployment.
const (
	// DefaultBucket is the heartbeat bucket: one live endpoint per instance.
	DefaultBucket = "ARCHIE_SERVICE_REGISTRY"

	// DefaultInstalledBucket is the durable "installed" bucket: one marker per
	// service that has ever registered, never expiring.
	DefaultInstalledBucket = "ARCHIE_SERVICE_INSTALLED"

	// DefaultTTL is how long a heartbeat entry survives without being
	// refreshed. It must comfortably exceed DefaultHeartbeat so a transient
	// pause in a live service's refresh does not drop it.
	DefaultTTL = 15 * time.Second

	// DefaultHeartbeat is the interval at which a registrar refreshes its
	// endpoint entry.
	DefaultHeartbeat = 5 * time.Second
)

// Config describes how to reach NATS and how the registry buckets behave.
// The zero value is not usable; call [Config.withDefaults] via [Connect], which
// fills every unset field.
type Config struct {
	// URL is the NATS server address. Required.
	URL string

	// Token authenticates the connection. Empty means no token auth.
	Token string

	// Bucket is the heartbeat bucket name.
	Bucket string

	// InstalledBucket is the durable "installed" bucket name.
	InstalledBucket string

	// TTL is how long a heartbeat entry survives without being refreshed. It
	// is the difference between a service that is installed-but-down (its
	// entries expired) and one that is live (its entries are fresh).
	TTL time.Duration

	// Heartbeat is the interval at which a registrar refreshes its entry. It
	// must be less than TTL, or a live service would expire between refreshes.
	Heartbeat time.Duration
}

// Validate reports whether the config can produce a usable client.
func (c Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("%w: URL is required", ErrInvalidConfig)
	}
	if c.TTL < 0 || c.Heartbeat < 0 {
		return fmt.Errorf("%w: durations must not be negative", ErrInvalidConfig)
	}
	if c.Heartbeat > 0 && c.TTL > 0 && c.Heartbeat >= c.TTL {
		return fmt.Errorf("%w: Heartbeat must be less than TTL", ErrInvalidConfig)
	}
	return nil
}

// withDefaults returns a copy with every unset field populated, so the rest of
// the package never has to test for zero values.
func (c Config) withDefaults() Config {
	if c.Bucket == "" {
		c.Bucket = DefaultBucket
	}
	if c.InstalledBucket == "" {
		c.InstalledBucket = DefaultInstalledBucket
	}
	if c.TTL == 0 {
		c.TTL = DefaultTTL
	}
	if c.Heartbeat == 0 {
		c.Heartbeat = DefaultHeartbeat
	}
	return c
}
