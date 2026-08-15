package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/gateway"
)

func TestName(t *testing.T) {
	g := New("", 0, nil, slog.Default())
	if g.Name() != "webhook" {
		t.Errorf("Name() = %q", g.Name())
	}
}

func TestValidateHMAC(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"key":"value"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !validateHMAC(body, validSig, secret) {
		t.Error("valid signature should pass")
	}
	if validateHMAC(body, validSig, "wrong-secret") {
		t.Error("wrong secret should fail")
	}
	if validateHMAC(body, "", secret) {
		t.Error("empty signature should fail")
	}
}

func TestExtractText(t *testing.T) {
	body := []byte(`{"issue":{"title":"fix bug","body":"details"}}`)

	if got := extractText(body, "issue.title"); got != "fix bug" {
		t.Errorf("extract issue.title = %q", got)
	}
	if got := extractText(body, "issue.body"); got != "details" {
		t.Errorf("extract issue.body = %q", got)
	}
	if got := extractText(body, ""); got != `{"issue":{"title":"fix bug","body":"details"}}` {
		t.Errorf("empty path should return raw body: %q", got)
	}
	if got := extractText(body, "nonexistent"); got != "" {
		t.Errorf("nonexistent path: %q", got)
	}
}

func TestWebhookHandlerMethodNotAllowed(t *testing.T) {
	g := New("", 0, []RouteConfig{{Path: "/hook"}}, slog.Default())
	handler := g.WebhookHandler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/hook", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestWebhookHandlerEmptyBody(t *testing.T) {
	g := New("", 0, []RouteConfig{{Path: "/hook"}}, slog.Default())
	handler := g.WebhookHandler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hook", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookHandlerNotStarted(t *testing.T) {
	g := New("", 0, []RouteConfig{{Path: "/hook"}}, slog.Default())
	handler := g.WebhookHandler()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"text":"hello"}`)
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hook", body))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestWebhookHandlerRoute(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	g := New("", 0, []RouteConfig{{Path: "/hook"}}, log)
	g.router = gateway.NewRouter(nil, nil, "webhook")

	handler := g.WebhookHandler()
	payload := `{"text":"hello from webhook"}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hook", strings.NewReader(payload)))
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestWebhookHandlerInvalidSignature(t *testing.T) {
	g := New("", 0, []RouteConfig{{Path: "/hook", Secret: "secret"}}, slog.Default())
	g.router = gateway.NewRouter(nil, nil, "webhook")

	handler := g.WebhookHandler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hook", strings.NewReader(`{"text":"x"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestWebhookHandlerValidSignature(t *testing.T) {
	secret := "secret"
	body := []byte(`{"text":"hello"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	g := New("", 0, []RouteConfig{{Path: "/hook", Secret: secret}}, slog.Default())
	g.router = gateway.NewRouter(nil, nil, "webhook")

	handler := g.WebhookHandler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hook", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", sig)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestWebhookStartStop(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	g := New("127.0.0.1", 18645, nil, log)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	router := gateway.NewRouter(nil, nil, "webhook")
	go func() { _ = g.Start(ctx, router) }()

	time.Sleep(30 * time.Millisecond)
	_ = g.Stop(context.Background())
}

func TestExtractTextJSONRoundTrip(t *testing.T) {
	data := []byte(`{"action":"push","repo":{"name":"archie-core"}}`)

	if got := extractText(data, "action"); got != "push" {
		t.Errorf("action = %q", got)
	}
	if got := extractText(data, "repo.name"); got != "archie-core" {
		t.Errorf("repo.name = %q", got)
	}
}

// Compile-time guard.
var _ gateway.Gateway = (*Gateway)(nil)
