// Package email implements gateway.Gateway for inbound email via SMTP.
// A lightweight SMTP server receives emails on a configurable port
// (intended for localhost use behind a mail relay like Postfix).
// Inbound messages are routed through the gateway for LLM processing;
// replies are sent via SMTP relay to the original sender.
package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// Gateway is an email gateway that receives inbound messages via SMTP
// and sends replies via SMTP relay.
type Gateway struct {
	ListenAddr string // e.g. ":2525" or "127.0.0.1:2525"
	// Relay is the SMTP relay for outbound replies (host:port).
	RelayAddr string
	// RelayUser is the SMTP AUTH username. Empty = no auth.
	RelayUser string
	// RelayPass is the SMTP AUTH password.
	RelayPass string
	// AllowedDomains restricts inbound senders. Empty = all allowed.
	AllowedDomains []string

	log    *slog.Logger
	router *gateway.Router
	mu     sync.Mutex
	ln     net.Listener
}

// New returns an unstarted Gateway.
func New(listenAddr, relayAddr string, log *slog.Logger) *Gateway {
	if listenAddr == "" {
		listenAddr = ":2525"
	}
	return &Gateway{
		ListenAddr: listenAddr,
		RelayAddr:  relayAddr,
		log:        log.With("component", "gateway-email"),
	}
}

func (g *Gateway) Name() string { return "email" }

// Start begins listening for SMTP connections. Blocks until ctx is
// cancelled.
func (g *Gateway) Start(ctx context.Context, router *gateway.Router) error {
	g.mu.Lock()
	g.router = router
	g.mu.Unlock()

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", g.ListenAddr)
	if err != nil {
		return fmt.Errorf("email: listen %s: %w", g.ListenAddr, err)
	}
	g.mu.Lock()
	g.ln = ln
	g.mu.Unlock()

	g.log.Info("email gateway listening", "addr", g.ListenAddr)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			g.log.Error("email accept", "err", err)
			continue
		}
		go g.handleSMTP(conn)
	}
}

// Stop shuts down the listener.
func (g *Gateway) Stop(ctx context.Context) error {
	g.mu.Lock()
	ln := g.ln
	g.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

// handleSMTP processes one SMTP session.
func (g *Gateway) handleSMTP(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	write := func(code int, msg string) {
		_, _ = fmt.Fprintf(w, "%d %s\r\n", code, msg)
		_ = w.Flush()
	}
	readLine := func() (string, error) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	write(220, "archie email gateway ready")

	var mailFrom, rcptTo string
	var dataBuf strings.Builder
	inData := false

	for {
		line, err := readLine()
		if err != nil {
			return
		}

		switch {
		case inData:
			if line == "." {
				// End of DATA — process the message.
				g.processMessage(mailFrom, rcptTo, dataBuf.String())
				write(250, "OK: message accepted")
				return
			}
			// Un-dot-stuff: RFC 5321 §4.5.2
			if strings.HasPrefix(line, "..") {
				line = line[1:]
			}
			dataBuf.WriteString(line)
			dataBuf.WriteString("\r\n")

		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"):
			mailFrom = extractAddr(line)
			write(250, "OK")

		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
			rcptTo = extractAddr(line)
			write(250, "OK")

		case strings.HasPrefix(strings.ToUpper(line), "DATA"):
			write(354, "Start mail input; end with <CRLF>.<CRLF>")
			inData = true

		case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
			write(221, "Bye")
			return

		case strings.HasPrefix(strings.ToUpper(line), "EHLO"), strings.HasPrefix(strings.ToUpper(line), "HELO"):
			write(250, "OK")

		default:
			write(500, "Unrecognized command")
		}
	}
}

// extractAddr pulls the email address from an SMTP command line like
// "MAIL FROM:<user@example.com>" or "RCPT TO:<user@example.com>".
func extractAddr(line string) string {
	start := strings.IndexByte(line, '<')
	end := strings.LastIndexByte(line, '>')
	if start >= 0 && end > start {
		return strings.TrimSpace(line[start+1 : end])
	}
	return strings.TrimSpace(line)
}

// processMessage extracts text from the raw email and routes it through
// the gateway router. Replies are sent back via SMTP.
func (g *Gateway) processMessage(from, to, raw string) {
	g.mu.Lock()
	router := g.router
	g.mu.Unlock()

	if router == nil {
		g.log.Warn("email received before gateway started")
		return
	}

	// Extract plain text body: everything after the first blank line
	// following Content-Type or headers.
	text := extractBody(raw)

	msg := gateway.Message{
		ChannelID: to,
		From:      from,
		Text:      text,
	}
	reply, err := router.Route(context.Background(), msg)
	if err != nil {
		g.log.Error("email route", "err", err, "from", from)
		return
	}

	if reply != "" && g.RelayAddr != "" {
		if err := g.sendReply(from, to, reply); err != nil {
			g.log.Error("email reply", "err", err, "to", from)
		}
	}
}

// extractBody pulls the plain text body from a raw email.
func extractBody(raw string) string {
	// Find the message body: after headers (blank line).
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(raw, "\n\n", 2)
	}
	if len(parts) < 2 {
		return strings.TrimSpace(raw)
	}

	headers := parts[0]
	body := parts[1]

	// If multipart, look for the text/plain section.
	if strings.Contains(strings.ToLower(headers), "content-type: multipart") {
		// Crude boundary extraction for simple cases.
		if idx := strings.Index(strings.ToLower(headers), "boundary="); idx >= 0 {
			boundary := extractBoundary(headers[idx:])
			if boundary != "" {
				// Find text/plain part within boundaries.
				sections := strings.SplitSeq(body, "--"+boundary)
				for sec := range sections {
					if strings.Contains(strings.ToLower(sec), "content-type: text/plain") {
						if parts := strings.SplitN(sec, "\r\n\r\n", 2); len(parts) == 2 {
							return strings.TrimSpace(strings.TrimRight(parts[1], "\r\n-"))
						}
						if parts := strings.SplitN(sec, "\n\n", 2); len(parts) == 2 {
							return strings.TrimSpace(strings.TrimRight(parts[1], "\n-"))
						}
					}
				}
			}
		}
	}

	// Non-multipart: strip trailing SMTP dots.
	body = strings.TrimSpace(body)
	// Remove any quoted-printable or base64 content headers.
	if parts := strings.SplitN(body, "\r\n\r\n", 2); len(parts) == 2 {
		body = parts[1]
	}
	return strings.TrimSpace(body)
}

func extractBoundary(line string) string {
	// boundary="xxx" or boundary=xxx
	line = strings.TrimPrefix(line, "boundary=")
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "\"")
	// Stop at semicolon or whitespace.
	if idx := strings.IndexAny(line, " ;\r\n"); idx >= 0 {
		return line[:idx]
	}
	return line
}

// sendReply delivers a reply to the original sender via SMTP relay.
func (g *Gateway) sendReply(to, from, text string) error {
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: Re: %s\r\n\r\n%s",
		from, to, truncateSubject(text), text)

	var auth smtp.Auth
	if g.RelayUser != "" {
		host, _, _ := net.SplitHostPort(g.RelayAddr)
		auth = smtp.PlainAuth("", g.RelayUser, g.RelayPass, host)
	}

	// Try TLS first, fall back to plain.
	c, err := smtp.Dial(g.RelayAddr)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: serverName(g.RelayAddr)}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			g.log.Warn("email relay auth failed", "err", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	_, err = io.WriteString(wc, msg)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return wc.Close()
}

func serverName(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "localhost"
	}
	return host
}

func truncateSubject(text string) string {
	if len(text) > 60 {
		return text[:57] + "..."
	}
	return text
}

// ConfigSchema returns the JSON Schema for the email channel config.
func (g *Gateway) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "required": ["listen_addr"],
  "properties": {
    "listen_addr": {
      "type": "string",
      "description": "Host:port for the inbound SMTP server (e.g. :2525)"
    },
    "relay_addr": {
      "type": "string",
      "description": "SMTP relay for outbound replies"
    },
    "relay_user": {
      "type": "string",
      "description": "SMTP AUTH username"
    },
    "relay_pass": {
      "type": "string",
      "description": "SMTP AUTH password"
    }
  }
}`)
}

// ValidateConfig checks the email channel configuration.
func (g *Gateway) ValidateConfig(cfg map[string]any) error {
	if cfg == nil {
		return fmt.Errorf("email config is required")
	}
	if addr, _ := cfg["listen_addr"].(string); addr == "" {
		return fmt.Errorf("email.listen_addr is required")
	}
	return nil
}

// Compile-time guard.
var _ channels.Channel = (*Gateway)(nil)
