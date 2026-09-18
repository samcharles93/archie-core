package archieui

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServeForceClosesWhenGracefulShutdownOverruns pins the shutdown behaviour
// behind the process smoke test (TestUIProcessServesTheDashboardAgainstLiveDependencies):
// when a graceful shutdown overruns its deadline because an in-flight request
// is still draining, serve must force-close the remaining connections and
// report a clean (nil) shutdown rather than a context.DeadlineExceeded that
// makes the process exit non-zero (archie-core-u4xu). The request is held open
// deterministically rather than by machine load, so the deadline path is
// driven directly.
func TestServeForceClosesWhenGracefulShutdownOverruns(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { close(release) })

	done := make(chan error, 1)
	go func() { done <- serve(ctx, listener, handler, Options{ShutdownTimeout: 50 * time.Millisecond}) }()

	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after graceful shutdown overran its deadline, want a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the shutdown deadline overran")
	}
}
