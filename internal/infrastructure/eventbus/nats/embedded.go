package nats

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// EmbeddedServer owns an in-process NATS server with JetStream enabled. It
// wraps nats-server/v2/server.Server so the SDK type never leaks past this
// package: callers get only the client URL and Shutdown, never the server.
//
// The daemon embeds one of these for single-process deployments so reaction
// delivery and task distribution share the same transport whether NATS runs
// as a separate server or in-process (docs/prds/embedded-nats.md). The daemon
// owns the lifecycle: StartEmbedded before dialing the client, Shutdown after
// the client closes.
type EmbeddedServer struct {
	srv *server.Server
	log *slog.Logger
}

// EmbeddedOptions configures StartEmbedded.
type EmbeddedOptions struct {
	// Host is the bind address. Empty means "127.0.0.1" (loopback only,
	// which is correct: an embedded server carries no authentication).
	Host string
	// Port is the listen port. Zero means a random port.
	Port int
	// StoreDir is where JetStream persists streams. Empty means a temporary
	// directory (streams do not survive a reboot). The daemon passes a dir
	// under its data directory so the ARCHIE_TASKS and reaction streams are
	// durable across daemon restarts.
	StoreDir string
}

// StartEmbedded starts an in-process NATS server with JetStream enabled and
// returns it once it is ready for connections. The caller owns the returned
// server and must call Shutdown to stop it.
func StartEmbedded(ctx context.Context, opts EmbeddedOptions, log *slog.Logger) (*EmbeddedServer, error) {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = -1 // random port, so two daemons on one host cannot collide
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	srv, err := server.NewServer(&server.Options{
		Host:      opts.Host,
		Port:      opts.Port,
		JetStream: true,
		StoreDir:  opts.StoreDir,
		NoSigs:    true, // the daemon owns signals, not the embedded server
		// NoLog leaves the server's logger nil (ConfigureLogger is only ever
		// called on reload). A nil logger makes the server's Fatalf a no-op
		// rather than an os.Exit, so a JetStream startup failure cannot crash
		// archied -- it surfaces instead as a Connect error below.
		NoLog: true,
	})
	if err != nil {
		return nil, fmt.Errorf("embedded nats create: %w", err)
	}
	srv.Start()

	// Start is asynchronous; wait for the accept loop. If the daemon's ctx is
	// already cancelled (shutdown racing boot), stop the server rather than
	// wait the full readiness window. A ctx cancelled during the wait is
	// tolerated: the wait is bounded, and a healthy server is ready in
	// milliseconds.
	if err := ctx.Err(); err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("embedded nats startup: %w", err)
	}
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		return nil, fmt.Errorf("embedded nats server did not become ready")
	}

	log.Info("embedded nats ready", "url", srv.ClientURL())
	return &EmbeddedServer{srv: srv, log: log}, nil
}

// ClientURL returns the address the server is listening on. It is the URL the
// client must dial, and the value the daemon records as the endpoint its own
// connection used.
func (e *EmbeddedServer) ClientURL() string { return e.srv.ClientURL() }

// Shutdown stops the server and waits for it to finish. It is safe to call on
// a nil server so callers can defer it unconditionally.
func (e *EmbeddedServer) Shutdown() {
	if e == nil || e.srv == nil {
		return
	}
	e.srv.Shutdown()
	e.log.Debug("embedded nats stopped")
}
