// Package nats implements service discovery on NATS JetStream KV.
//
// A heartbeat bucket holds one TTL'd key per live instance,
// "<service>.<instance-id>". An installed bucket holds one permanent marker
// per service, "<service>". A service with a marker and no live instances is
// installed with no endpoints; one with no marker is not installed. Names
// and IDs must not contain dots.
package nats
