package archied

import (
	"context"
	"log/slog"
	"time"
)

// controlPlaneWatchRetry is how long a watch waits before re-establishing a
// stream that ended. The minimum is the delay a healthy attempt leaves behind,
// and the delay a failed attempt doubles; the cap bounds the delay, never the
// retrying: a watch reconnects for as long as the process lives.
//
// A control plane that is down is retried slowly enough that it is not a
// request loop, and the charge reaches the shapes of outage an attempt that
// ENDS can take: a store that errors at once is charged, and so is one that
// stalls for less than the window the attempt is judged against, because that
// window is derived from the delay the attempt would otherwise be charged and
// widens with the ladder instead of sitting at its floor (attemptHealthy). The
// reach stops at the window: a stall still going when the window closes is read
// as a store that answered, so an outage whose every attempt takes at least
// that long leaves the ladder at the minimum -- deliberately, and pinned by
// TestKeepWatchTreatsAStallPastTheWindowAsAStoreThatAnswered.
//
// This loop charges only attempts that end, and imposes no timeout of its own:
// nextUpdate waits on the stream and on the loop's own context, never on a
// clock, so a peer that accepts the stream and then never answers it ends
// nothing here and this code has nothing to charge. What ends that stream is
// the transport's client keepalive,
// internal/infrastructure/staterpc/dial.go (Time 10s, Timeout 5s): the
// unacknowledged PING tears the connection down, the watch stream errors, the
// attempt ends, and here it is charged like any other attempt that ended. A
// hung peer is that keepalive's business, not this loop's. The watch rides
// that connection because the daemon takes its control-plane client off the
// *staterpc.Client the dial returned, on the same connection the keepalive was
// installed on (internal/app/archied/bootstrap.go, openStateStoreAdapter,
// which reads client.ControlPlane()).
const (
	controlPlaneWatchRetryMin = 250 * time.Millisecond
	controlPlaneWatchRetryMax = 30 * time.Second
)

// controlPlaneWatchHealthyMultiple multiplies the delay an attempt would
// otherwise be charged into the window that attempt must stay open to be read
// as a store that answered and had nothing to say. It is strictly more than
// one, so that window is always wider than the charge itself: a window equal to
// the delay would read a store that fails as slowly as that delay as healthy on
// every attempt, which is the fixed-rate loop this rule exists to charge.
const controlPlaneWatchHealthyMultiple = 2

// keepWatch delivers every update from first and, when that stream ends,
// re-establishes it in this same goroutine until ctx ends.
//
// A control-plane watch stream is not permanent. The State Store restarts, the
// connection drops, the server closes the stream -- and nothing on the client
// side reopens it. A watch that ends on its first end stops applying that kind
// for the rest of the process's life: every later stored change is silently
// never applied while the last apply-status record keeps re-stamping, so the
// settings page reads the process as current, and the process recovers only by
// restarting (archie-core-yrmr).
//
// One goroutine per kind, reconnecting in place: a long outage does not stack
// watchers. Reopening resumes after the last version an update carried, so a
// reconnect neither replays a document this process has already handled over a
// newer one nor skips one it has not. A transport failure carries no version
// and so leaves the resume point alone. A document the client refused to decode
// does carry one -- controlplane.Client decodes what the store sends, and the
// store answered with that version -- and the resume point moves past it
// deliberately: that version is unusable to this build, and resuming before it
// would re-offer it on every reconnect for the life of the process. Only a
// cancellation stops the loop, and the loop watches for it itself, so shutdown
// is prompt however long the current backoff is and whether or not the stream
// is still open.
//
// The reconnect delay is decided by the attempt that just ended, and only by
// that (attemptHealthy, retryDelay): a healthy attempt returns it to the
// minimum, and a failed attempt doubles it up to the cap. An attempt is not
// healthy because a reopen was accepted. A control plane accepts the stream
// before it reads anything -- that is how gRPC hands a request to its handler
// -- and controlplane.Server.Watch answers a store it cannot read by returning
// an error, so during the outage this loop exists for every reopen is accepted
// and every stream ends carrying nothing: judged on the acceptance alone the
// delay stays at the minimum and the watch becomes a 4 RPCs/second request
// loop.
//
// The rule is one sentence: an attempt is healthy iff it delivered an update
// this watch had not handled, or it was still open when the window closed
// (attemptHealthy, controlPlaneWatchHealthyWindow -- the delay the attempt would
// otherwise be charged, multiplied by a factor strictly greater than one, so
// the window is always wider than that charge). The window is derived from that
// charge rather than fixed at the minimum, which is what carries the rule past
// the shape an immediate error does not cover: a store that has stopped
// answering does not error at once, it stalls, and an attempt that stalls for
// less than the window is charged and the delay climbs a rung.
//
// The window is not a read timeout, and this loop imposes none of its own (see
// controlPlaneWatchRetry for what ends an attempt against a peer that never
// answers), so the reach of the rule stops at the window itself: a stall still
// going when the window closes is read as a store that answered, and the delay
// stays at the minimum for as long as every attempt takes that long. That
// is deliberate rather than a gap to close with another rule. The stalled
// attempt lasts the window, so the reopens it leaves behind are bounded by the
// attempt and not by the delay -- about 0.8 a second at the minimum rung,
// against the four a second this rule exists to charge -- and a shape that slow
// is not the request loop it exists for. It is pinned by
// TestKeepWatchTreatsAStallPastTheWindowAsAStoreThatAnswered.
//
// open, versionOf and deliver are the kind's halves. open re-establishes the
// stream -- the caller opens the first one itself, so that a control plane
// which cannot be watched at all still fails the boot that asked for it.
// versionOf reads the resource version an update carries. deliver applies the
// update, including reporting the error it carries, which is how a refused
// document reaches the settings page.
//
// wait is the delay between a stream ending and the reopen that follows it, and
// production passes waitFor. It is injected so that the delay the loop hands it
// is observable as a value: the retry_in log line states the delay the loop
// charged, and a loop that charged one delay and waited another would leave that
// attribute agreeing with itself. What the injected wait cannot show is a
// multiple taken inside waitFor itself, which is a timer for exactly the d it is
// given (TestKeepWatchWaitsTheDelayItCharged reads the argument, not the wait).
func keepWatch[T any](
	ctx context.Context,
	log *slog.Logger,
	kind string,
	after int64,
	first <-chan T,
	open func(ctx context.Context, afterVersion int64) (<-chan T, error),
	wait func(ctx context.Context, d time.Duration) bool,
	versionOf func(update T) int64,
	deliver func(update T),
) {
	stream, backoff := first, controlPlaneWatchRetryMin
	for {
		started, progressed := time.Now(), false
		for {
			update, ok := nextUpdate(ctx, stream)
			if !ok {
				break
			}
			deliver(update)
			if version := versionOf(update); version > after {
				after, progressed = version, true
			}
		}
		if ctx.Err() != nil {
			return
		}
		backoff = retryDelay(backoff, attemptHealthy(progressed, time.Since(started), backoff))
		log.Warn("control plane watch stream ended; reconnecting",
			"kind", kind, "after_version", after, "retry_in", backoff)
		for {
			if !wait(ctx, backoff) {
				return
			}
			reopened, err := open(ctx, after)
			if err == nil {
				stream = reopened
				break
			}
			// A reopen the control plane refused is the store still not
			// answering, and is charged exactly as any other attempt that ends
			// that way.
			backoff = retryDelay(backoff, false)
			log.Warn("control plane watch could not be re-established",
				"kind", kind, "after_version", after, "retry_in", backoff, "err", err)
		}
	}
}

// attemptHealthy is what one attempt that ended makes of the reconnect delay.
// An attempt is healthy iff it delivered an update this watch had not handled
// -- the store answered, whatever the stream did afterwards -- or it was still
// open when controlPlaneWatchHealthyWindow closed, at that boundary and not
// only past it, which is a store that had nothing to say and was still there.
// An attempt that did neither delivered nothing and died inside the window it
// would have been charged: the stream was accepted and the store was not
// answering, which is a failed attempt and the only thing the delay grows on.
func attemptHealthy(progressed bool, openFor, backoff time.Duration) bool {
	return progressed || openFor >= controlPlaneWatchHealthyWindow(backoff)
}

// controlPlaneWatchHealthyWindow is how long an attempt that delivered nothing
// must stay open to count as a store that answered and had nothing to say: the
// delay the attempt would otherwise be charged, multiplied by
// controlPlaneWatchHealthyMultiple. The window moves with the ladder, so the
// charge reaches a store that fails slowly at every rung of it, which a window
// fixed at the minimum does not. The multiple is strictly more than one, so the
// window is always wider than that charge -- a store that fails as slowly as
// the charge it would be paid is still charged, which is the shape of
// TestWatchRetryGrowsWhileTheStateStoreIsDown that says so -- and an attempt is
// healthy at the window as well as past it (TestAttemptHealthyAtTheWindowBoundary
// pins that edge).
func controlPlaneWatchHealthyWindow(backoff time.Duration) time.Duration {
	return controlPlaneWatchHealthyMultiple * retryDelay(backoff, false)
}

// retryDelay is the delay the attempt that just ended leaves behind: a healthy
// attempt returns it to the minimum, so an outage is charged for itself and not
// for the quiet time that preceded it, and a failed attempt doubles it up to
// the cap, so a store that stays gone is retried ever more slowly.
func retryDelay(current time.Duration, healthy bool) time.Duration {
	if healthy {
		return controlPlaneWatchRetryMin
	}
	return min(2*current, controlPlaneWatchRetryMax)
}

// nextUpdate receives the next update from stream, reporting false when the
// stream ended or when ctx ended first. A watch's stream is closed by
// controlplane.Client when the stream's own context ends, but shutdown must not
// rest on that cross-package coupling: a stream that never closed would
// otherwise hold the watch -- and the process that owns it -- past the
// cancellation that asked it to stop. The coupling that is load-bearing runs
// the other way: this watch returns on cancellation without draining what the
// producer has left for it, so controlplane.Client's producer must stop on the
// same context instead of parking on a send no reader will ever take (its send
// guard is pinned by TestKeepWatchLeavesNoProducerParkedOnAnUndeliveredUpdate).
func nextUpdate[T any](ctx context.Context, stream <-chan T) (T, bool) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, false
	case update, ok := <-stream:
		return update, ok
	}
}

// waitFor is the wait production injects into keepWatch. It waits for d,
// reporting false when ctx ended first: a watch's backoff must never delay the
// shutdown of the process that owns it.
func waitFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
