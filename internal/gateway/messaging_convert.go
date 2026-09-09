package gateway

// This file holds the one piece of the old channel-facing Message that
// outlived it: the role derivation.
//
// messaging.Message is the canonical persisted record and is now also the
// currency at the channel-facing boundary. Channel adapters build it
// directly and set Role themselves (always messaging.RoleUser -- a channel
// carries only what a person said), and the gateway sets
// messaging.RoleAssistant on the reply it generates, so nothing converts
// between two shapes of a message in process any more.
//
// The wire is the exception. pb.Message carries no role, so both sides of
// the gRPC contract derive it from the owning session's bot identity
// instead of adding a field to the proto -- see
// internal/infrastructure/gatewayrpc. Because both derive from the same
// session, they agree.
