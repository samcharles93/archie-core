package nats

import "errors"

// ErrInvalidConfig reports a Config that cannot produce a usable client.
var ErrInvalidConfig = errors.New("nats: invalid configuration")
