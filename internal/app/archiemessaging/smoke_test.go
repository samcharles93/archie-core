package archiemessaging

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// recordingChatContract records the turn the Messaging Service dispatched and
// answers it, so a round trip can be asserted on what actually crossed the
// service boundary rather than on what a channel meant to send.
type recordingChatContract struct {
	messaging.ChatContract
	routes chan messaging.Inbound
	reply  string
}

func (c *recordingChatContract) Route(_ context.Context, in messaging.Inbound) (messaging.ChatReply, error) {
	c.routes <- in
	return messaging.ChatReply{Text: c.reply, SessionID: "smoke-session"}, nil
}

// freePort reserves a loopback port and releases it, so the composed service can
// be given a concrete address. A webhook bound to port 0 composes fine, but
// nothing exposes the port the listener actually received, and a round trip needs
// somewhere to send the request.
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	bound, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not a TCP address", listener.Addr())
	}
	return bound.Port
}

// waitForListener polls until something accepts on addr, so the request below
// races the channel's own goroutine rather than the clock.
func waitForListener(t *testing.T, addr string) {
	t.Helper()
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := dialer.DialContext(t.Context(), "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing accepted on %s within the deadline", addr)
}

func sign(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookChannelSmokeRoundTrip is archie-core-8cda.6.5's end-to-end smoke
// verification: the real composition, the real webhook adapter, a real listener,
// and a real signed HTTP request -- asserted on the inbound the service hands its
// chat contract and on the reply the caller receives back.
//
// Webhook is the channel this can be done for without external services. Telegram
// needs a bot token and the Telegram API and email needs SMTP; neither is
// something a test can own. docs/prds/messaging-service-boundary.md's fourth
// criterion asks for a live Telegram round trip "or equivalent channel smoke
// test", and this is that equivalent -- every step between the POST and the
// contract is the deployed code path. The gRPC hop beyond the contract is covered
// by internal/infrastructure/gatewayrpc's conformance tests, which exercise the
// same client and server in both local and remote mode.
func TestWebhookChannelSmokeRoundTrip(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	const (
		routePath = "/smoke"
		secret    = "smoke-secret"
		reply     = "ack from the contract"
	)
	body := []byte(`{"issue":{"title":"a smoke-test issue"}}`)

	contract := &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: reply}
	srv, err := compose(t.Context(), deps{
		Config: ResolvedConfig{
			Options:       Options{ShutdownTimeout: 2 * time.Second},
			WebhookAddr:   addr,
			Webhook:       config.WebhookRoute{Path: routePath, Template: "issue.title", DeliverTo: "origin"},
			WebhookSecret: secret,
		},
		Log:  slog.Default(),
		Chat: contract,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- srv.Start(ctx) }()
	t.Cleanup(func() {
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

	waitForListener(t, addr)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://"+addr+routePath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("X-Hub-Signature-256", sign(t, secret, body))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post to the webhook channel: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	// The reply travelled back: DeliverTo=origin means the contract's answer is the
	// HTTP response body, so this asserts the whole path rather than half of it.
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (body %q)", response.StatusCode, got)
	}
	if strings.TrimSpace(string(got)) != reply {
		t.Errorf("response body = %q, want the contract's reply %q", got, reply)
	}

	select {
	case in := <-contract.routes:
		if in.Message.Text != "a smoke-test issue" {
			t.Errorf("Text = %q, want the templated title", in.Message.Text)
		}
		if in.Message.Role != messaging.RoleUser {
			t.Errorf("Role = %q, want the channel's messages to arrive as a user's", in.Message.Role)
		}
		if in.Message.ConversationID.ChannelID != routePath {
			t.Errorf("ChannelID = %q, want the configured route path %q", in.Message.ConversationID.ChannelID, routePath)
		}
		// The three transport facts this channel must carry, each of which has a
		// reason recorded elsewhere: the platform is how the Gateway learns which
		// channel it serves (archie-core-c1qx); SenderID stays empty because a route
		// path is a source and not a person (archie-core-oyna); and the route
		// survives as the budget key so the inbound limit still applies.
		if in.Platform != "webhook" {
			t.Errorf("Platform = %q, want %q", in.Platform, "webhook")
		}
		if in.Message.SenderID != "" {
			t.Errorf("SenderID = %q, want empty: a route path must never become a user identity", in.Message.SenderID)
		}
		if in.BudgetKey != routePath {
			t.Errorf("BudgetKey = %q, want the route path %q", in.BudgetKey, routePath)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the webhook channel never reached the chat contract")
	}
}
