package relay

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln := listen(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func startRelay(t *testing.T, target string) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, target) }()
	return ln.Addr().String(), cancel, done
}

func TestRelayForwardsBothWays(t *testing.T) {
	addr, cancel, _ := startRelay(t, echoServer(t))
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	for _, line := range []string{"first\n", "second\n"} {
		if _, err := io.WriteString(c, line); err != nil {
			t.Fatal(err)
		}
		got, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || got != line {
			t.Fatalf("echo %q, want %q (%v)", got, line, err)
		}
	}
}

func TestRelayClosesTheClientWhenTheTargetIsDown(t *testing.T) {
	dead := listen(t)
	target := dead.Addr().String()
	_ = dead.Close()
	addr, cancel, _ := startRelay(t, target)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read from a relay with no target: %v, want EOF", err)
	}
}

func TestRelayStopsOnCancel(t *testing.T) {
	_, cancel, done := startRelay(t, echoServer(t))
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}
