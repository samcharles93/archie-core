package nats

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// EmbeddedServer is an in-process NATS server with JetStream. Start it
// before dialing and shut it down after the client closes.
type EmbeddedServer struct {
	srv   *server.Server
	log   *slog.Logger
	token string
}

// EmbeddedOptions configures StartEmbedded.
type EmbeddedOptions struct {
	// Host is the bind address. Empty means "127.0.0.1" (loopback only).
	// Container-backed deployments pass the host gateway address of the
	// Docker bridge their workers join.
	Host string
	// Port is the listen port. Zero means a random port.
	Port int
	// Token is the instance token: every permission. Empty generates one.
	Token string
	// Tasks admits task users ("task-<id>" with the run credential) to their
	// own task's subjects. Nil admits the instance token only.
	Tasks TaskAuthority
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
	token := opts.Token
	if token == "" {
		token = rand.Text()
	}

	srv, err := server.NewServer(&server.Options{
		Host:      opts.Host,
		Port:      opts.Port,
		JetStream: true,
		StoreDir:  opts.StoreDir,
		// Replaces token authorization: the instance token and task users
		// are both checked here.
		CustomClientAuthentication: authenticator{token: token, tasks: opts.Tasks},
		NoSigs:                     true, // the daemon owns signals, not the embedded server
		// The custom logger below reports fatal errors without exiting archied.
		NoLog: true,
	})
	if err != nil {
		return nil, fmt.Errorf("embedded nats create: %w", err)
	}
	srv.SetLogger(brokerLogger{log: log.With("service", "nats", "component", "nats")}, false, false)
	srv.Start()

	// Wait for the server to accept connections, or stop it if ctx is already
	// cancelled.
	if err := ctx.Err(); err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("embedded nats startup: %w", err)
	}
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		return nil, fmt.Errorf("embedded nats server did not become ready")
	}

	log.Info("embedded nats ready", "url", srv.ClientURL())
	return &EmbeddedServer{srv: srv, log: log, token: token}, nil
}

// ClientURL returns the address the server is listening on. It is the URL the
// client must dial, and the value the daemon records as the endpoint its own
// connection used.
func (e *EmbeddedServer) ClientURL() string { return e.srv.ClientURL() }

// Token returns the per-start credential required by every embedded NATS
// client. Composition keeps it in memory and passes it only to the daemon's
// client and managed worker environments; it must never be logged or persisted.
func (e *EmbeddedServer) Token() string { return e.token }

// Shutdown stops the server and waits for it to finish. It is safe to call on
// a nil server so callers can defer it unconditionally.
func (e *EmbeddedServer) Shutdown() {
	if e == nil || e.srv == nil {
		return
	}
	e.srv.Shutdown()
	e.log.Debug("embedded nats stopped")
}

// Fatal broker errors stay local instead of terminating the host process.
type brokerLogger struct{ log *slog.Logger }

func (l brokerLogger) Noticef(format string, args ...any) { l.log.Info(fmt.Sprintf(format, args...)) }

func (l brokerLogger) Warnf(format string, args ...any) { l.log.Warn(fmt.Sprintf(format, args...)) }

func (l brokerLogger) Errorf(format string, args ...any) { l.log.Error(fmt.Sprintf(format, args...)) }

func (l brokerLogger) Fatalf(format string, args ...any) { l.log.Error(fmt.Sprintf(format, args...)) }

func (l brokerLogger) Debugf(format string, args ...any) { l.log.Debug(fmt.Sprintf(format, args...)) }

func (l brokerLogger) Tracef(format string, args ...any) { l.log.Debug(fmt.Sprintf(format, args...)) }
