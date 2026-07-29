// Package eventbus is the broker-neutral messaging contract.
//
// It defines only the messaging behaviour Archie requires: publication,
// subscription, request/reply, and delivery outcomes. It names no broker.
// NATS subjects, JetStream configuration, consumer groups, acknowledgement
// mechanics, reconnection and serialization live in
// internal/infrastructure/eventbus/nats, which implements these interfaces.
//
// Per docs/architecture/dependencies-and-contracts.md, domains depend on this
// package and never on an infrastructure implementation; only application
// composition chooses a concrete broker.
//
// # The bus does not own message meaning
//
// This package deliberately transports opaque payloads. Message schemas
// belong to the domain that defines them -- work intake owns its task
// envelope, agent execution owns its request and response types -- and each
// domain owns the subjects it addresses. A previous version of the NATS
// package exported a TaskEnvelope type and a PublishTask method, which made
// the transport responsible for knowing what a unit of work was.
//
// Subjects are plain strings here. Constraining their syntax would encode one
// broker's addressing rules into the contract.
package eventbus
