package nats

import "errors"

// Sentinel errors callers are expected to match with errors.Is. Anything else
// returned by this package is wrapped with operation context via %w.
var (
	// ErrNoMessage reports that a fetch completed normally but no message was
	// available before the poll deadline. It is an expected outcome of polling
	// an idle queue, not a failure -- the previous API signalled this with a
	// (nil, nil) return, which callers had to remember to check for.
	ErrNoMessage = errors.New("nats: no message available")

	// ErrNotConnected reports use of a client whose connection is closed or
	// was never established.
	ErrNotConnected = errors.New("nats: not connected")

	// ErrNoReplyAddress reports a request message that carried no reply
	// inbox in its headers, so no response can be routed back.
	ErrNoReplyAddress = errors.New("nats: message has no reply address")

	// ErrInvalidConfig reports a Config that cannot produce a usable client.
	ErrInvalidConfig = errors.New("nats: invalid configuration")

	// ErrUnknownTaskKind reports a publish whose TaskKind has no subject.
	// Routing to the default queue instead would silently misdeliver work.
	ErrUnknownTaskKind = errors.New("nats: unknown task kind")
)
