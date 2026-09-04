// Package servicediscovery is the broker-neutral service registry contract.
//
// It defines only the shape archie-core needs to discover and watch the
// services of its decomposed fleet: a way to resolve a named service to its
// live endpoints, and a way to watch that membership change over time. It
// names no registry. A NATS JetStream KV implementation lives in
// internal/infrastructure/servicediscovery/nats; the Kubernetes-native
// primary (Service/EndpointSlice + CoreDNS, per
// docs/inspiration/service-discovery-research-2026-09-05.md) is the sibling
// that serves the same contract.
//
// Per docs/architecture/dependencies-and-contracts.md, consumers depend on
// this package and never on an infrastructure implementation; only
// application composition chooses a concrete registry.
//
// # NotInstalled is not Down
//
// The single most important semantic in this package is the distinction
// between a service that was never installed and a service that is installed
// but currently has no healthy endpoint.
//
//   - [ErrNotInstalled] reports that a service has never been installed. It
//     is a distinct outcome, not a failure. An optional service (Curator
//     today, others later) that is not present must resolve to "absent",
//     never "broken": callers match it with errors.Is and treat it as "this
//     capability is disabled", not "this capability is unhealthy".
//   - A service that IS installed but has zero healthy endpoints right now is
//     a normal, non-error outcome. [ServiceRegistry.Resolve] returns a
//     possibly-empty slice with a nil error; [ServiceRegistry.Watch] returns
//     a live channel that simply does not emit until an endpoint joins.
//
// This is why a registry implementation must model presence separately from
// liveness. The NATS implementation keeps a durable "installed" marker that
// never expires, distinct from expiring heartbeat keys: the marker's presence
// means NotInstalled=false, while heartbeat expiry is Down/Unhealthy.
package servicediscovery
