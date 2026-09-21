package staterpc

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

// tcpUserTimeout is TCP_USER_TIMEOUT (Linux, include/uapi/linux/tcp.h). The
// stdlib syscall package does not name it; grpc sets it from the client
// keepalive timeout on every transport it creates with keepalive enabled
// (internal/syscall.SetTCPUserTimeout), which makes it the fastest observable
// proof that a connection dialed here carries keepalive at all.
const tcpUserTimeout = 18

// TestDialInstallsClientKeepalive covers the half-open-connection defect: with
// no client keepalive the dialed connection carries a keepalive time of
// infinity, so a peer that stops delivering bytes (peer gone, socket still
// ESTABLISHED) never fails, and a silent control-plane Watch stays frozen for
// the life of the process. The assertion is on the dialed socket: grpc only
// sets TCP_USER_TIMEOUT from the keepalive timeout when keepalive is enabled,
// so a zero value here means the dial installed no keepalive and the defect is
// back.
func TestDialInstallsClientKeepalive(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	// The accepted peer never writes the HTTP/2 preface, so the dialed call
	// below blocks in its connection attempt; grpc has already applied the
	// socket options by then, which is all this test reads.
	dialed := make(chan *net.TCPConn, 1)
	client, cleanup, err := Dial(listener.Addr().String(), "", grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			select {
			case dialed <- tcp:
			default:
			}
		}
		return conn, nil
	}))
	if err != nil {
		t.Fatalf("dial state store: %v", err)
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _, _ = client.ControlPlane().Catalog(ctx, &controlpb.CatalogRequest{}) }()
	var conn *net.TCPConn
	select {
	case conn = <-dialed:
	case <-ctx.Done():
		t.Fatal("the dial never opened a connection to the peer")
	}

	// grpc sets the option while creating the transport, after the dialer
	// returns, so read it once the connect attempt is under way.
	deadline := time.Now().Add(2 * time.Second)
	var got int
	for {
		got = userTimeout(t, conn)
		if got != 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != 5000 {
		t.Fatalf("dialed connection TCP_USER_TIMEOUT = %d ms, want 5000 (the keepalive timeout): the dial installed no client keepalive, so nothing detects a half-open connection", got)
	}
}

// userTimeout reads TCP_USER_TIMEOUT in milliseconds off conn's socket.
func userTimeout(t *testing.T, conn *net.TCPConn) int {
	t.Helper()
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatalf("raw connection: %v", err)
	}
	value := -1
	if err := raw.Control(func(fd uintptr) {
		value, err = syscall.GetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeout)
	}); err != nil {
		t.Fatalf("read TCP_USER_TIMEOUT: %v", err)
	}
	return value
}
