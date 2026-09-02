package email

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/gateway"
)

func TestName(t *testing.T) {
	g := New(":2525", "", slog.Default())
	if g.Name() != "email" {
		t.Errorf("Name() = %q", g.Name())
	}
}

func TestExtractAddr(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MAIL FROM:<user@example.com>", "user@example.com"},
		{"RCPT TO:<test@foo.bar>", "test@foo.bar"},
		{"MAIL FROM:<user@example.com> SIZE=1234", "user@example.com"},
		{"garbage", "garbage"},
	}
	for _, tt := range tests {
		if got := extractAddr(tt.input); got != tt.want {
			t.Errorf("extractAddr(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractBodyPlain(t *testing.T) {
	raw := "From: test@example.com\r\nSubject: Hello\r\n\r\nThis is the body text.\r\nMore text."
	if got := extractBody(raw); !strings.Contains(got, "This is the body") {
		t.Errorf("extractBody = %q", got)
	}
}

func TestExtractBodyMultipart(t *testing.T) {
	raw := "From: test@example.com\r\nContent-Type: multipart/alternative; boundary=abc123\r\n\r\n--abc123\r\nContent-Type: text/plain\r\n\r\nplain text here\r\n--abc123\r\nContent-Type: text/html\r\n\r\n<html></html>\r\n--abc123--"
	if got := extractBody(raw); got != "plain text here" {
		t.Errorf("extractBody = %q, want 'plain text here'", got)
	}
}

func TestExtractBoundary(t *testing.T) {
	if got := extractBoundary(`boundary="abc123"`); got != "abc123" {
		t.Errorf("got %q", got)
	}
	if got := extractBoundary(`boundary=xyz`); got != "xyz" {
		t.Errorf("got %q", got)
	}
}

func TestSMTPReceiveAndRoute(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	g := New(":0", "", log)

	router := gateway.NewRouter(nil, func(ctx context.Context, msg gateway.Message) (string, error) {
		if msg.From != "sender@test.com" {
			return "", fmt.Errorf("unexpected sender: %s", msg.From)
		}
		return "got it", nil
	}, "email")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Start the server to get the assigned port, then connect.
	var listenConfig net.ListenConfig
	ln, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	g.ListenAddr = addr
	go func() { _ = g.Start(ctx, router, gateway.Lifecycle{}) }()
	time.Sleep(20 * time.Millisecond)
	defer func() { _ = g.Stop(context.Background()) }()

	// Connect via SMTP and send a message.
	dialer := net.Dialer{Timeout: 1 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Read greeting.
	buf := make([]byte, 1024)
	n, _ := conn.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), "220") {
		t.Fatalf("unexpected greeting: %q", string(buf[:n]))
	}

	// Send a minimal SMTP transaction.
	send := func(s string) {
		_, _ = conn.Write([]byte(s + "\r\n"))
		time.Sleep(5 * time.Millisecond)
	}

	send("EHLO test")
	send("MAIL FROM:<sender@test.com>")
	send("RCPT TO:<archie@local>")
	send("DATA")
	send("Hello from email test.")
	send(".")
	send("QUIT")

	// Drain response.
	time.Sleep(50 * time.Millisecond)
}

func TestStopUnstarted(t *testing.T) {
	g := New(":2525", "", slog.Default())
	if err := g.Stop(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendReplyNoRelay(t *testing.T) {
	g := New(":2525", "", slog.Default())
	// Should not panic when relay is empty.
	if err := g.sendReply("to@x.com", "from@x.com", "test"); err == nil {
		t.Log("expected error with empty relay")
	}
}

func TestTruncateSubject(t *testing.T) {
	if got := truncateSubject("hello"); got != "hello" {
		t.Errorf("short: %q", got)
	}
	long := strings.Repeat("x", 100)
	if got := truncateSubject(long); len(got) > 60 {
		t.Errorf("long: %d chars", len(got))
	}
}

// Compile-time guard.
var (
	_ gateway.Gateway = (*Gateway)(nil)
	_                 = smtp.PlainAuth
)
