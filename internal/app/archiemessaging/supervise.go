package archiemessaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/channels"
)

// runChannel runs one channel and starts its replacement after a requested
// restart. A channel that exits on its own is not restarted.
func (s *Service) runChannel(ctx context.Context, c *channelInstance) {
	defer s.wg.Done()
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
		// observed as a channel that is no longer supervised.
		c.mu.Lock()
		c.cancel = nil
		shutdown := ctx.Err() != nil
		restart := c.restartPending
		if !shutdown && restart {
			c.restartPending = false
		}
		if shutdown || !restart {
			c.supervised = false
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
			// The replacement is already in c.channel (see restartChannel); the
			// next pass starts it.
			continue
		}
		// The channel stopped on its own.
		if err != nil {
			s.status.MarkFailed(c.name, err.Error())
			s.log.Error("channel stopped with error", "name", c.name, "err", err)
		} else {
			s.status.MarkStopped(c.name, "")
		}
		return
	}
}

// restartChannel builds a replacement for channel id from next, then stops
// the old one and starts it. A channel not running is not restarted.
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
	c.restartPending = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
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
