package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/servicediscovery"
)

// Register announces service's instance ep with the registry and keeps its
// heartbeat fresh until the returned Registration is unregistered or ctx is
// cancelled.
//
// It writes the service's durable installed marker once (idempotent: an
// existing marker is left alone) -- that marker is what makes the service
// "installed" rather than NotInstalled -- then writes a live heartbeat entry
// for ep and refreshes it on an interval. If the service's process stops
// without calling Unregister (its ctx is cancelled, or it crashes), the entry
// expires via the bucket TTL and watchers observe a Leave; only an explicit
// uninstall of the whole service removes the installed marker.
//
// ep.ID must be non-empty and must not contain the registry key separator; the
// service name must likewise avoid the separator (see the package doc).
func (c *Client) Register(ctx context.Context, service string, ep servicediscovery.Endpoint) (*Registration, error) {
	if ep.ID == "" {
		return nil, fmt.Errorf("register %s: %w: endpoint ID is required", service, ErrInvalidConfig)
	}
	if strings.Contains(ep.ID, keySeparator) || strings.Contains(service, keySeparator) {
		return nil, fmt.Errorf("register %s: %w: service and instance ID must not contain %q", service, ErrInvalidConfig, keySeparator)
	}

	if err := c.ensureInstalled(ctx, service); err != nil {
		return nil, err
	}

	key := endpointKey(service, ep.ID)
	value, err := encodeEndpoint(ep)
	if err != nil {
		return nil, err
	}
	if _, err := c.kv.Put(ctx, key, value); err != nil {
		return nil, fmt.Errorf("register %s/%s: %w", service, ep.ID, err)
	}

	hbCtx, cancel := context.WithCancel(ctx)
	reg := &Registration{
		service:   service,
		key:       key,
		value:     value,
		kv:        c.kv,
		heartbeat: c.cfg.Heartbeat,
		log:       c.log,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	go reg.heartbeatLoop(hbCtx)
	return reg, nil
}

// ensureInstalled writes the service's installed marker if it is absent. The
// marker has no TTL, so it persists for the life of the install; a service that
// registered once but has no live instance is still installed (and resolves to
// an empty, non-error roster), not NotInstalled.
func (c *Client) ensureInstalled(ctx context.Context, service string) error {
	if _, err := c.installed.Create(ctx, service, installedMarkerValue); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil
		}
		return fmt.Errorf("mark %s installed: %w", service, err)
	}
	return nil
}

// Registration is one live announcement of a service instance. It owns the
// background heartbeat that keeps the instance's entry fresh; stopping it is
// the caller's responsibility via Unregister (or by cancelling the ctx passed
// to Register, which stops the heartbeat and lets the entry expire).
type Registration struct {
	service   string
	key       string
	value     []byte
	kv        jetstream.KeyValue
	heartbeat time.Duration
	log       *slog.Logger
	cancel    context.CancelFunc
	done      chan struct{}
	once      sync.Once
}

// heartbeatLoop refreshes the instance's live entry on an interval until ctx is
// cancelled. It never stops on a single refresh failure -- a transient broker
// blip must not unregister a healthy instance -- only on cancellation.
func (rg *Registration) heartbeatLoop(ctx context.Context) {
	defer close(rg.done)
	ticker := time.NewTicker(rg.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := rg.refresh(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				rg.log.Warn("heartbeat refresh failed", "service", rg.service, "key", rg.key, "error", err)
			}
		}
	}
}

// refresh re-publishes the instance's entry, resetting its bucket-TTL window so
// it stays live.
func (rg *Registration) refresh(ctx context.Context) error {
	if _, err := rg.kv.Put(ctx, rg.key, rg.value); err != nil {
		return fmt.Errorf("refresh %s: %w", rg.key, err)
	}
	return nil
}

// Unregister removes the instance's live entry and stops its heartbeat. It is
// idempotent: a second call is a no-op that returns nil. The service's durable
// installed marker is left in place, so the service remains "installed" but
// down until an explicit uninstall.
func (rg *Registration) Unregister(ctx context.Context) error {
	var unregErr error
	rg.once.Do(func() {
		// Stop the heartbeat first and wait for it to finish, so no in-flight
		// refresh can re-create the entry after we delete it.
		rg.cancel()
		<-rg.done

		if err := rg.kv.Delete(ctx, rg.key); err != nil {
			// An already-expired entry is a normal leave, not a failure.
			if !errors.Is(err, jetstream.ErrKeyNotFound) && !errors.Is(err, jetstream.ErrKeyDeleted) {
				unregErr = err
			}
		}
	})
	if unregErr != nil {
		return fmt.Errorf("unregister %s: %w", rg.key, unregErr)
	}
	return nil
}
