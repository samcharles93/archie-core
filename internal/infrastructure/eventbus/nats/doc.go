// Package nats is the NATS JetStream transport for task distribution and
// agent communication.
//
// It is infrastructure: it owns the broker connection, stream and consumer
// lifecycle, and the wire encoding of messages. It does not own workflow
// routing, task state, or any other domain behaviour.
//
// The package deliberately keeps NATS SDK types behind its own boundary.
// Callers receive [Message], not jetstream.Msg, and publish [TaskEnvelope] or
// [Request], not *nats.Msg. That keeps the broker replaceable and stops SDK
// types leaking into domain packages, per
// docs/architecture/dependencies-and-contracts.md.
//
// Layout:
//
//	config.go    connection and stream/consumer tuning
//	errors.go    sentinel errors callers match on
//	subjects.go  subject naming, pure and dependency-free
//	message.go   wire payloads and the Message wrapper
//	client.go    connection lifecycle
//	publisher.go publishing
//	consumer.go  fetching
//
// When no NATS URL is configured the daemon uses its local SQLite claim flow
// instead; nothing here is required for single-process operation.
package nats
