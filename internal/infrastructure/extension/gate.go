package extension

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

// gate is the host end of one extension's egress: a Unix socket serving an
// allow-list proxy. A path socket is reachable from the extension's network
// namespace, where nothing else outside it is.
type gate struct {
	dir    string
	socket string
	server *http.Server
}

func openGate(ctx context.Context, allow []string) (*gate, error) {
	dir, err := os.MkdirTemp("", "archie-ext-")
	if err != nil {
		return nil, err
	}
	g := &gate{dir: dir, socket: filepath.Join(dir, "egress.sock")}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", g.socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	g.server = &http.Server{Handler: egress.NewTunnel(allow), ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := g.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = ln.Close()
		}
	}()
	return g, nil
}

func (g *gate) close() {
	_ = g.server.Close()
	_ = os.RemoveAll(g.dir)
}
