package nats

import "errors"

// ErrInvalidConfig reports a Config that cannot produce a usable client.
//
// The delivery-outcome sentinel a caller matches on -- NotInstalled -- belongs
// to the broker-neutral contract in internal/servicediscovery, so a caller can
// handle it without knowing which registry is in use. This package returns
// that, not a copy of it.
var ErrInvalidConfig = errors.New("nats: invalid configuration")
