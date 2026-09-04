// Package nats is the NATS JetStream KV transport for service discovery.
//
// It implements the broker-neutral
// [servicediscovery.ServiceRegistry] contract against a JetStream KeyValue
// store, and additionally provides the registration side a service uses to
// announce itself. It is the fallback discovery mechanism for single-host,
// no-Kubernetes installs (Path B of
// docs/inspiration/service-discovery-research-2026-09-05.md); the
// Kubernetes-native primary is a sibling implementation of the same contract.
//
// # Two buckets: presence versus liveness
//
// The NotInstalled/Down distinction (the governing semantic of
// [servicediscovery]) requires modelling a service's presence separately from
// any instance's liveness. NATS KV cannot express that with one bucket,
// because a bucket-wide TTL (needed so stale heartbeats expire) would also
// expire the "installed" record, collapsing Down into NotInstalled. So the
// implementation uses two buckets:
//
//   - Registry bucket (heartbeats): one key per live instance, refreshed on a
//     heartbeat interval and expired by a bucket-wide TTL. Presence of a key
//     here means the instance is live.
//   - Installed bucket (marker): one key per service, written once at first
//     registration with no TTL, never expires. Presence of this marker means
//     the service was installed; absence means never installed => NotInstalled.
//
// A service that registered once but whose instances have all gone is still
// installed (its marker persists) and simply has zero live endpoints, so
// [Client.Resolve] returns an empty slice with no error. Only a service that
// never wrote a marker resolves to [servicediscovery.ErrNotInstalled].
//
// # Key scheme
//
// Instance heartbeat keys are "<service>.<instance-id>" (dot-separated), and
// the installed marker key is "<service>". The dot separator is a deliberate
// deviation from the research sketch's "<service>/<instance-id>": NATS KV
// watch filters match on dot-separated subject tokens, so a per-service watch
// of "curator.*" must see "curator.inst-1" as two tokens. A slash would make
// the whole key a single opaque token that no per-service wildcard can match.
// Consequently both service names and instance IDs must avoid the dot
// character; standard slugs and UUIDs satisfy that.
//
// Layout:
//
//	config.go    connection and bucket tuning, defaults and validation
//	errors.go    sentinel errors callers match on
//	keys.go      key scheme, pure and dependency-free
//	registry.go  the ServiceRegistry client (Resolve/Watch)
//	registrar.go the registration side (announce + heartbeat)
package nats
