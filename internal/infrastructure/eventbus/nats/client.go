package nats

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

// Compile-time proof that Client satisfies the broker-neutral contract.
// The interfaces live in internal/eventbus so domains can depend on the
// behaviour without importing this package.
var _ eventbus.Bus = (*Client)(nil)

// Client owns the NATS connection and its JetStream stream and consumer.
type Client struct {
	conn     *nats.Conn
	js       jetstream.JetStream
	stream   jetstream.Stream
	consumer jetstream.Consumer
	cfg      Config
	log      *slog.Logger
}

// Connect dials the broker and provisions the stream and pull consumer. The
// stream uses file storage and the retention policy from Config.Retention
// (defaulting to work-queue for task distribution).
//
// On any failure the partially-built connection is closed before returning, so
// a failed Connect leaks nothing.
func Connect(ctx context.Context, cfg Config, log *slog.Logger) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
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

	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       cfg.StreamName,
		Subjects:   cfg.Subjects,
		Storage:    jetstream.FileStorage,
		Retention:  *cfg.Retention,
		Duplicates: cfg.DedupWindow,
	})
	if err != nil {
		return nil, fmt.Errorf("nats create stream %q: %w", cfg.StreamName, err)
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:              cfg.ConsumerName,
		Durable:           cfg.ConsumerName,
		FilterSubject:     cfg.FilterSubject,
		AckPolicy:         jetstream.AckExplicitPolicy,
		MaxDeliver:        cfg.MaxDeliver,
		AckWait:           cfg.AckWait,
		InactiveThreshold: cfg.InactiveTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("nats create consumer %q: %w", cfg.ConsumerName, err)
	}

	closeOnFail = nil
	log.Info("nats connected",
		"stream", cfg.StreamName,
		"consumer", cfg.ConsumerName,
		"url", conn.ConnectedUrl())

	return &Client{
		conn:     conn,
		js:       js,
		stream:   stream,
		consumer: consumer,
		cfg:      cfg,
		log:      log,
	}, nil
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
// callers holding an optional bus can defer it unconditionally.
func (c *Client) Close() {
	if c == nil || c.conn == nil {
		return
	}
	c.conn.Close()
	c.log.Debug("nats connection closed")
}

// CoreConn exposes the raw connection for the NATS-specific RPC layers
// (forgerpc, storerpc, worktreerpc, natsrpc) that register their own core-NATS
// subscriptions.
//
// This is the single sanctioned escape hatch and exists only because those
// packages are themselves NATS infrastructure, not domain code. Domain and
// application packages must use [Publisher], [Consumer] or [Requester]
// instead; taking the connection there would put SDK types back into their
// imports, which is what this package exists to prevent.
func (c *Client) CoreConn() (*nats.Conn, error) { return c.connection() }

// connection returns the live connection, or ErrNotConnected.
func (c *Client) connection() (*nats.Conn, error) {
	if c == nil || c.conn == nil || c.conn.IsClosed() {
		return nil, eventbus.ErrNotConnected
	}
	return c.conn, nil
}
