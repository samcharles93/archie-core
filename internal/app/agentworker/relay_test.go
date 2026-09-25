package agentworker

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func echoTarget(t *testing.T, reply string) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(c, reply)
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestRunRelayForwardsEachListenerToItsTarget(t *testing.T) {
	proxy, nats := freeAddr(t), freeAddr(t)
	forwards := map[string]string{proxy: echoTarget(t, "proxy"), nats: echoTarget(t, "nats")}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- RunRelay(ctx, forwards) }()

	for listen, want := range map[string]string{proxy: "proxy", nats: "nats"} {
		var got []byte
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listen)
			if err == nil {
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				got, _ = io.ReadAll(c)
				_ = c.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("relay never listened on %s: %v", listen, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if string(got) != want {
			t.Fatalf("%s reached %q, want %q", listen, got, want)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunRelay after cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunRelay did not stop on cancel")
	}
}

func TestRunRelayFailsWhenAListenerCannotBind(t *testing.T) {
	taken, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	err = RunRelay(t.Context(), map[string]string{taken.Addr().String(): "127.0.0.1:1", freeAddr(t): "127.0.0.1:1"})
	if err == nil {
		t.Fatal("RunRelay started with a listener that could not bind")
	}
}
