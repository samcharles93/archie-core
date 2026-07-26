// Package webhook implements gateway.Gateway for inbound HTTP webhooks.
// External services push messages into archie-core's gateway for LLM
// processing. Outbound delivery is not supported (deliver-only mode).
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// Gateway is a deliver-only webhook gateway. It listens for HTTP POST
// requests, validates their HMAC signatures, and routes the extracted
// text through the gateway.Router for LLM processing.
type Gateway struct {
	Host   string
	Port   int
	Routes []RouteConfig
	log    *slog.Logger

	server *http.Server
	router *gateway.Router
	mu     sync.Mutex
}

// RouteConfig defines one webhook endpoint.
type RouteConfig struct {
	// Path is the URL path (e.g. "/github").
	Path string
	// Secret is the HMAC-SHA256 secret for payload validation.
	// Empty = no validation.
	Secret string
	// Template extracts the message text from the JSON payload.
	// Uses dot-notation: "issue.title" or "comment.body".
	// Empty = use the raw body as message text.
	Template string
	// DeliverTo routes replies: "origin" (HTTP response) or empty (no reply).
	DeliverTo string
}

// New returns an unstarted Gateway. Call Start to begin listening.
func New(host string, port int, routes []RouteConfig, log *slog.Logger) *Gateway {
	if host == "" {
		host = "0.0.0.0"
	}
	return &Gateway{
		Host:   host,
		Port:   port,
		Routes: routes,
		log:    log.With("component", "gateway-webhook"),
	}
}

func (g *Gateway) Name() string { return "webhook" }

// Start begins listening on the configured host:port. Blocks until ctx
// is cancelled.
func (g *Gateway) Start(ctx context.Context, router *gateway.Router) error {
	g.mu.Lock()
	g.router = router

	mux := http.NewServeMux()
	for i := range g.Routes {
		route := g.Routes[i] // capture
		mux.HandleFunc(route.Path, g.handleWebhook(&route))
	}

	addr := fmt.Sprintf("%s:%d", g.Host, g.Port)
	g.server = &http.Server{Addr: addr, Handler: mux}
	g.mu.Unlock()

	g.log.Info("webhook gateway listening", "addr", addr)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.server.Shutdown(shutdownCtx)
	}()

	err := g.server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("webhook: listen: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (g *Gateway) Stop(ctx context.Context) error {
	g.mu.Lock()
	srv := g.server
	g.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// WebhookHandler returns the HTTP handler for testing.
func (g *Gateway) WebhookHandler() http.Handler {
	mux := http.NewServeMux()
	for i := range g.Routes {
		route := g.Routes[i]
		mux.HandleFunc(route.Path, g.handleWebhook(&route))
	}
	return mux
}

func (g *Gateway) handleWebhook(route *RouteConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB
		if err != nil {
			g.log.Error("webhook read body", "err", err)
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		// HMAC validation.
		if route.Secret != "" {
			sig := r.Header.Get("X-Hub-Signature-256")
			if sig == "" {
				sig = r.Header.Get("X-Signature-256")
			}
			if !validateHMAC(body, sig, route.Secret) {
				g.log.Warn("webhook invalid signature", "path", route.Path)
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}

		// Extract message text.
		text := extractText(body, route.Template)
		if text == "" {
			http.Error(w, "empty message", http.StatusBadRequest)
			return
		}

		// Route through the gateway.
		g.mu.Lock()
		router := g.router
		g.mu.Unlock()

		if router == nil {
			http.Error(w, "not started", http.StatusServiceUnavailable)
			return
		}

		msg := gateway.Message{
			ChannelID: route.Path,
			From:      "webhook",
			Text:      text,
		}
		reply, err := router.Route(r.Context(), msg)
		if err != nil {
			g.log.Error("webhook route", "err", err, "path", route.Path)
			http.Error(w, "route error", http.StatusInternalServerError)
			return
		}

		if route.DeliverTo == "origin" && reply != "" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(reply))
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	}
}

// validateHMAC checks a SHA-256 HMAC signature against the body.
func validateHMAC(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}
	// Strip "sha256=" prefix if present.
	sigHex := strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sigHex), []byte(expected))
}

// extractText pulls a value from a JSON body using dot-notation path.
// "issue.title" → body["issue"]["title"]. Empty path = raw body.
func extractText(body []byte, path string) string {
	if path == "" {
		return strings.TrimSpace(string(body))
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return strings.TrimSpace(string(body))
	}
	parts := strings.Split(path, ".")
	var current any = data
	for _, p := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[p]
	}
	if s, ok := current.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
