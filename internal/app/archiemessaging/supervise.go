package archiemessaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/channels"
)

// runChannel supervises one channel for the service's lifetime. It starts the
// channel and, when the run ends, decides what happens next: a requested
// restart starts the replacement, a run that failed is retried with bounded
// backoff, and a channel that keeps failing is parked until an explicit
// restart. Only shutdown ends the supervisor, so a channel that stops is never
// left without one to start it again.
func (s *Service) runChannel(ctx context.Context, c *channelInstance) {
	defer s.wg.Done()
	defer c.supervisorExits()
	attempts := 0
	for {
		c.mu.Lock()
		ch := c.channel
		runCtx, cancel := context.WithCancel(ctx)
		c.cancel = cancel
		c.mu.Unlock()

		s.log.Info("starting channel", "name", c.name)
		err := ch.Start(runCtx, s.chat, s.lifecycleFor(c.name))

		// Decide the next state under the same lock a restart takes, so a
		// restart racing this exit is either seen here (restartPending) or
		// delivered to the wait below (notify).
		c.mu.Lock()
		c.cancel = nil
		shutdown := ctx.Err() != nil
		restart := c.restartPending
		if !shutdown && restart {
			c.restartPending = false
		}
		c.mu.Unlock()
		cancel()

		// Stop is safe to call after the run context was cancelled: each
		// channel's Start returns having closed what it opened, and Stop is the
		// idempotent tail for the paths that leave a listener behind.
		s.stopChannel(ctx, c.name, ch)

		if shutdown {
			if err != nil && !errors.Is(err, context.Canceled) {
				s.status.MarkFailed(c.name, err.Error())
			} else {
				s.status.MarkStopped(c.name, "")
			}
			return
		}
		if restart {
			attempts = 0
			continue
		}
		if err == nil {
			// The channel stopped itself. Hold it stopped rather than
			// spending the failure retry budget on a run that chose to end.
			s.status.MarkStopped(c.name, "")
			s.log.Info("channel stopped", "name", c.name)
			if s.awaitChannel(ctx, c, 0) == channelShutdown {
				return
			}
			attempts = 0
			continue
		}
		attempts++
		s.status.MarkFailed(c.name, err.Error())
		if attempts >= s.retry.attempts {
			s.log.Error("channel failed repeatedly; parked until an explicit restart",
				"name", c.name, "attempts", attempts, "err", err)
			if s.awaitChannel(ctx, c, 0) == channelShutdown {
				return
			}
			attempts = 0
			continue
		}
		backoff := s.retry.backoffFor(attempts)
		s.log.Warn("channel failed; restarting", "name", c.name,
			"attempt", attempts, "of", s.retry.attempts, "in", backoff, "err", err)
		switch s.awaitChannel(ctx, c, backoff) {
		case channelShutdown:
			return
		case channelRestart:
			attempts = 0
		}
	}
}

// channelRetry bounds how a supervisor retries a channel that fails on its own
// before parking it for an explicit restart.
type channelRetryPolicy struct {
	attempts   int
	backoff    time.Duration
	maxBackoff time.Duration
}

// defaultChannelRetry is the retry policy a composed service uses.
func defaultChannelRetry() channelRetryPolicy {
	return channelRetryPolicy{attempts: 5, backoff: time.Second, maxBackoff: 30 * time.Second}
}

// backoffFor is the wait before attempt n (1-based): doubling from backoff,
// capped at maxBackoff so a long outage does not become a long silence between
// retries.
func (r channelRetryPolicy) backoffFor(attempt int) time.Duration {
	backoff := r.backoff << (attempt - 1)
	if backoff <= 0 || backoff > r.maxBackoff {
		return r.maxBackoff
	}
	return backoff
}

// channelWait is how a supervisor's wait between runs ended.
type channelWait int

const (
	// channelRetry: the backoff elapsed; run the channel again.
	channelRetry channelWait = iota
	// channelRestart: an explicit restart arrived; start the replacement.
	channelRestart
	// channelShutdown: the service is stopping.
	channelShutdown
)

// awaitChannel blocks until an explicit restart arrives, the service stops, or
// the backoff elapses. A delay of zero waits with no timer: the parked state
// after a channel stopped or gave up, which only a restart or shutdown ends.
func (s *Service) awaitChannel(ctx context.Context, c *channelInstance, delay time.Duration) channelWait {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return channelShutdown
		case <-c.notify:
			return channelRestart
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return channelShutdown
	case <-c.notify:
		return channelRestart
	case <-timer.C:
		// A restart delivered as the timer fired is read here rather than
		// lost between the two branches.
		select {
		case <-c.notify:
			return channelRestart
		default:
			return channelRetry
		}
	}
}

// supervisorExits records that a channel's supervisor is leaving, so a restart
// after shutdown is refused rather than signalling a goroutine that is gone.
func (c *channelInstance) supervisorExits() {
	c.mu.Lock()
	c.supervised = false
	c.mu.Unlock()
}

// restartChannel builds a replacement for channel id from next and starts it in
// place of the running one. A supervisor that is between runs -- backing off a
// failure, or parked after giving up -- is woken to start the replacement; a
// channel with no supervisor (never started, or shut down) is refused.
func (s *Service) restartChannel(id string, next ResolvedConfig) error {
	c := s.instanceByID(id)
	if c == nil {
		return fmt.Errorf("no channel %q", id)
	}
	if c.rebuild == nil {
		return fmt.Errorf("channel %q cannot be rebuilt", id)
	}
	replacement, err := c.rebuild(next)
	if err != nil {
		return fmt.Errorf("rebuild %q: %w", id, err)
	}
	if replacement == nil {
		return fmt.Errorf("channel %q has no configuration", id)
	}
	c.mu.Lock()
	if !c.supervised {
		c.mu.Unlock()
		return fmt.Errorf("channel %q is not running", id)
	}
	c.channel = replacement
	cancel := c.cancel
	if cancel != nil {
		// A run is in flight. Cancelling its context ends Start, and the
		// supervisor sees restartPending and starts the replacement.
		c.restartPending = true
	} else {
		// The supervisor is between runs. Wake it so it starts the replacement
		// now, rather than waiting out its backoff or its park.
		c.restartPending = false
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		return nil
	}
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return nil
}

// stopChannel closes one channel's listener with its own bounded context, so a
// stop that hangs cannot hold the shutdown past the configured timeout. The
// parent context is stripped of its cancellation because this runs on the
// shutdown path, where the parent is already cancelled.
func (s *Service) stopChannel(ctx context.Context, name string, ch channels.Channel) {
	timeout := s.currentConfig().Options.ShutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := ch.Stop(stopCtx); err != nil {
		s.log.Warn("channel stop failed", "name", name, "err", err)
	}
}

func (s *Service) instanceByID(id string) *channelInstance {
	for _, c := range s.channels {
		if c.name == id {
			return c
		}
	}
	return nil
}

func (s *Service) currentConfig() ResolvedConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Service) setConfig(cfg ResolvedConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}
