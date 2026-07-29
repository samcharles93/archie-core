package nats

import "errors"

// ErrInvalidConfig reports a Config that cannot produce a usable client.
//
// The delivery-outcome sentinels a caller matches on -- no message, not
// connected, no reply address -- belong to the broker-neutral contract in
// internal/eventbus, so a caller can handle them without knowing which broker
// is in use. This package returns those, not copies of them.
var ErrInvalidConfig = errors.New("nats: invalid configuration")
