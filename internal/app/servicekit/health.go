package servicekit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServeHealth serves mux on addr in the background and returns the function
// that shuts it down. label names the surface in log and error text.
func ServeHealth(ctx context.Context, addr string, mux http.Handler, label string, log *slog.Logger) (func(), error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for %s: %w", label, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(label+" server stopped", "err", err)
		}
	}()
	log.Info(label+" listening", "addr", "http://"+listener.Addr().String())
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}, nil
}
