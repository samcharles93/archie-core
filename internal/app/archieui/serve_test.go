package archieui

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestServeReportsCleanShutdown pins the shutdown behaviour behind the process
// smoke test (TestUIProcessServesTheDashboardAgainstLiveDependencies): serve
// must report a clean (nil) shutdown both when a graceful shutdown drains its
// in-flight request within ShutdownTimeout and when the deadline overruns and
// the remaining connection is force-closed (archie-core-u4xu). The request is
// held open deterministically rather than by machine load, so each deadline
// path is driven directly instead of depending on scheduling.
func TestServeReportsCleanShutdown(t *testing.T) {
	tests := []struct {
		name            string
		shutdownTimeout time.Duration
		// holdPastTimeout keeps the request in flight until serve force-closes
		// it, instead of releasing it as soon as shutdown begins.
		holdPastTimeout bool
	}{
		{
			name:            "request drains within ShutdownTimeout",
			shutdownTimeout: 5 * time.Second,
		},
		{
			name:            "request still in flight when ShutdownTimeout elapses",
			shutdownTimeout: 50 * time.Millisecond,
			holdPastTimeout: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}

			started := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			t.Cleanup(releaseRequest)

			done := make(chan error, 1)
			go func() { done <- serve(ctx, listener, handler, Options{ShutdownTimeout: tt.shutdownTimeout}) }()

			reqErr := make(chan error, 1)
			go func() {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
				if err != nil {
					reqErr <- err
					return
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					reqErr <- err
					return
				}
				_ = resp.Body.Close()
				reqErr <- nil
			}()

			// Wait for the request to reach the handler. A request that never
			// lands must fail promptly rather than hang until the package
			// timeout, and a serve that exits before the request lands must
			// also fail rather than deadlock.
			select {
			case <-started:
			case err := <-reqErr:
				t.Fatalf("request did not reach the handler: %v", err)
			case err := <-done:
				if err == nil {
					t.Fatal("serve returned cleanly before the request landed")
				}
				t.Fatalf("serve returned before the request landed: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("request did not reach the handler before the wait deadline")
			}

			cancel()

			if !tt.holdPastTimeout {
				releaseRequest()
			}

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("serve returned %v after graceful shutdown, want a clean shutdown", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("serve did not return after shutdown")
			}
		})
	}
}
