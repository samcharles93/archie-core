package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/servicediscovery"
)

// Compile-time proof that Client satisfies the broker-neutral contract. The
// interface lives in internal/servicediscovery so domains can depend on the
// behaviour without importing this package.
var _ servicediscovery.ServiceRegistry = (*Client)(nil)

// installedMarkerValue is the value stored at an installed-bucket key. Only its
// presence matters; the value exists so the bucket is self-describing.
var installedMarkerValue = []byte("installed")

// Client implements servicediscovery.ServiceRegistry and registration over
// the heartbeat and installed buckets.
type Client struct {
	conn      *nats.Conn
	kv        jetstream.KeyValue // heartbeat bucket
	installed jetstream.KeyValue // installed bucket
	cfg       Config
	log       *slog.Logger
}

// Connect dials NATS and creates the heartbeat bucket (with TTL) and the
// installed bucket (without). On failure it closes the connection.
func Connect(ctx context.Context, cfg Config, log *slog.Logger) (*Client, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	conn, err := dial(cfg)
	if err != nil {
		return nil, err
	}

	// Every failure past this point must close conn. Cleared on success.
	closeOnFail := conn.Close
	defer func() {
		if closeOnFail != nil {
			closeOnFail()
		}
	}()

	js, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("nats jetstream init: %w", err)
	}

	kv, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:         cfg.Bucket,
		History:        1,
		TTL:            cfg.TTL,
		LimitMarkerTTL: markerTTL(cfg.TTL),
	})
	if err != nil {
		return nil, fmt.Errorf("nats create registry bucket %q: %w", cfg.Bucket, err)
	}

	installed, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  cfg.InstalledBucket,
		History: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("nats create installed bucket %q: %w", cfg.InstalledBucket, err)
	}

	closeOnFail = nil
	log.Info("service registry connected",
		"url", conn.ConnectedUrl(),
		"bucket", cfg.Bucket,
		"installed_bucket", cfg.InstalledBucket)

	return &Client{
		conn:      conn,
		kv:        kv,
		installed: installed,
		cfg:       cfg,
		log:       log,
	}, nil
}

// markerTTL returns the subject-delete-marker TTL for the registry bucket. A
// heartbeat expiry is surfaced to watchers as a delete marker, but NATS rejects
// a marker TTL below one second; clamp the configured TTL up to that floor so a
// fast-expiring bucket still lets watchers observe the leave.
func markerTTL(ttl time.Duration) time.Duration {
	if ttl < time.Second {
		return time.Second
	}
	return ttl
}

// dial opens the raw connection, applying auth options from cfg.
func dial(cfg Config) (*nats.Conn, error) {
	var opts []nats.Option
	if cfg.Token != "" {
		opts = append(opts, nats.Token(cfg.Token))
	}
	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", cfg.URL, err)
	}
	return conn, nil
}

// Close shuts the connection down. It is safe to call on a nil client so
// callers holding an optional registry can defer it unconditionally.
func (c *Client) Close() {
	if c == nil || c.conn == nil {
		return
	}
	c.conn.Close()
	c.log.Debug("service registry connection closed")
}

// Resolve returns service's live endpoints, possibly none. It returns
// servicediscovery.ErrNotInstalled when the service has no marker.
func (c *Client) Resolve(ctx context.Context, service string) ([]servicediscovery.Endpoint, error) {
	if err := c.requireInstalled(ctx, service); err != nil {
		return nil, err
	}

	lister, err := c.kv.ListKeysFiltered(ctx, endpointKeyPrefix(service))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", service, err)
	}
	defer func() { _ = lister.Stop() }()

	// A lister may report duplicate keys; dedupe by ID and sort for a stable
	// order so callers get a deterministic roster.
	byID := map[string]servicediscovery.Endpoint{}
	for key := range lister.Keys() {
		entry, err := c.kv.Get(ctx, key)
		if err != nil {
			// A key may expire between listing and Get; that is a healthy
			// leave, not a failure.
			if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
				continue
			}
			return nil, fmt.Errorf("resolve %s: %w", service, err)
		}
		if entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge {
			continue
		}
		ep, ok := decodeEndpoint(entry.Value())
		if !ok {
			c.log.Warn("discarding undecodable endpoint", "service", service, "key", key)
			continue
		}
		ep.Service = service
		byID[ep.ID] = ep
	}

	eps := make([]servicediscovery.Endpoint, 0, len(byID))
	for _, ep := range byID {
		eps = append(eps, ep)
	}
	slices.SortFunc(eps, func(a, b servicediscovery.Endpoint) int {
		return strings.Compare(a.ID, b.ID)
	})
	return eps, nil
}

// Watch emits Join and Leave events for service until ctx ends, then closes
// the channel. It returns servicediscovery.ErrNotInstalled when the service
// has no marker.
func (c *Client) Watch(ctx context.Context, service string) (<-chan servicediscovery.Event, error) {
	if err := c.requireInstalled(ctx, service); err != nil {
		return nil, err
	}

	watcher, err := c.kv.Watch(ctx, endpointKeyPrefix(service))
	if err != nil {
		return nil, fmt.Errorf("watch %s: %w", service, err)
	}

	out := make(chan servicediscovery.Event)
	go c.fanWatch(ctx, service, watcher, out)
	return out, nil
}

// fanWatch converts KV updates to Join and Leave events until ctx ends,
// ignoring heartbeat refreshes.
func (c *Client) fanWatch(ctx context.Context, service string, watcher jetstream.KeyWatcher, out chan<- servicediscovery.Event) {
	defer close(out)
	defer func() { _ = watcher.Stop() }()

	known := map[string]struct{}{}
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-watcher.Updates():
			if !ok {
				return
			}
			// A nil entry marks the end of the initial snapshot; membership
			// events continue from there.
			if entry == nil {
				continue
			}
			ev, ok := eventFromEntry(service, entry)
			if !ok {
				c.log.Warn("discarding unrecognised registry entry",
					"service", service, "key", entry.Key(), "op", entry.Operation())
				continue
			}
			id := ev.Endpoint.ID
			switch ev.Kind {
			case servicediscovery.Join:
				if _, seen := known[id]; seen {
					continue
				}
				known[id] = struct{}{}
			case servicediscovery.Leave:
				if _, seen := known[id]; !seen {
					continue
				}
				delete(known, id)
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

// requireInstalled checks the installed marker for service, translating absence
// into the contract's ErrNotInstalled so the NotInstalled/Down distinction is
// expressed by both Resolve and Watch.
func (c *Client) requireInstalled(ctx context.Context, service string) error {
	if _, err := c.installed.Get(ctx, service); err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return fmt.Errorf("%s: %w", service, servicediscovery.ErrNotInstalled)
		}
		return fmt.Errorf("%s: %w", service, err)
	}
	return nil
}

// eventFromEntry converts a KV entry to an event: a Put is a Join, a delete
// or expiry a Leave. It reports false for an entry it cannot read.
func eventFromEntry(service string, entry jetstream.KeyValueEntry) (servicediscovery.Event, bool) {
	switch entry.Operation() {
	case jetstream.KeyValuePut:
		ep, ok := decodeEndpoint(entry.Value())
		if !ok {
			return servicediscovery.Event{}, false
		}
		ep.Service = service
		return servicediscovery.Event{Kind: servicediscovery.Join, Endpoint: ep}, true
	case jetstream.KeyValueDelete, jetstream.KeyValuePurge:
		id := idFromEndpointKey(service, entry.Key())
		if id == "" {
			return servicediscovery.Event{}, false
		}
		return servicediscovery.Event{
			Kind:     servicediscovery.Leave,
			Endpoint: servicediscovery.Endpoint{Service: service, ID: id},
		}, true
	default:
		return servicediscovery.Event{}, false
	}
}
