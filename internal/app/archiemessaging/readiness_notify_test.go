package archiemessaging

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/sdnotify"
)

// notifyListener is a stand-in for systemd's NOTIFY_SOCKET: a unixgram socket
// that collects the state lines the service sends. It is bound to the real
// NOTIFY_SOCKET for the duration of the test.
type notifyListener struct {
	conn *net.UnixConn
}

// listenNotify binds a notify socket in the test's temp dir and points
// NOTIFY_SOCKET at it. A unix socket path is capped near 108 bytes, so the
// short file name is what keeps the path inside t.TempDir() under that.
func listenNotify(t *testing.T) *notifyListener {
	t.Helper()
	addr := filepath.Join(t.TempDir(), "n.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen for notify datagrams at %q: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	t.Setenv(sdnotify.NotifySocketEnv, addr)
	return &notifyListener{conn: conn}
}

// waitForState reads datagrams until one carries want or the window closes.
func (l *notifyListener) waitForState(t *testing.T, window time.Duration, want string) bool {
	t.Helper()
	deadline := time.Now().Add(window)
	buf := make([]byte, 1024)
	for time.Now().Before(deadline) {
		if err := l.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			t.Fatalf("set notify read deadline: %v", err)
		}
		n, _, err := l.conn.ReadFromUnix(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			t.Fatalf("read notify datagram: %v", err)
		}
		if string(buf[:n]) == want {
			return true
		}
	}
	return false
}

// TestServiceAnnouncesReadyWhenServing: archie-messaging run as a Type=notify
// unit must send READY=1 at the moment it declares itself serving, or systemd
// kills a healthy process once TimeoutStartSec expires. It could not before:
// the client lived in internal/app/archied, which this process cannot import
// (archie-core-g5g8). The announcement is judged against the same evidence the
// other serve paths use -- the process itself says it is serving -- and not
// against a second notion of started invented here.
func TestServiceAnnouncesReadyWhenServing(t *testing.T) {
	listener := listenNotify(t)

	srv, err := compose(t.Context(), deps{
		Config: ResolvedConfig{Options: Options{ShutdownTimeout: 2 * time.Second}},
		Log:    slog.New(slog.DiscardHandler),
		Chat:   &dummyChatContract{},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan error, 1)
	go func() { started <- srv.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		srv.Stop()
		select {
		case err := <-started:
			if err != nil {
				t.Errorf("srv.Start returned %v, want a clean stop", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("srv.Start did not return after Stop, so a channel leaked its goroutine")
		}
	})

	if !listener.waitForState(t, 5*time.Second, sdnotify.ReadyState) {
		t.Fatal("no READY=1 datagram within 5s of the Messaging Service serving")
	}
}
