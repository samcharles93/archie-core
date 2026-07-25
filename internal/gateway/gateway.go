// Package gateway defines the persistent-connection layer between archie
// and its users. Each gateway implementation (Telegram, web UI, Discord,
// etc.) owns its connection lifecycle and delegates message dispatch to a
// shared CommandRouter.
//
// The router distinguishes gateway-local commands — handled directly
// without the LLM (model changes, status queries, restart) — from
// general messages that need LLM processing.
package gateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Message is an inbound message from a gateway connection.
type Message struct {
	// ChannelID identifies the conversation within the channel (e.g. a
	// Telegram chat ID). Replies are sent back to this ID.
	ChannelID string
	// From identifies the sender (channel-specific: username, user ID).
	From string
	// Text is the raw message text, including any leading slash command.
	Text string
}

// A Gateway owns a persistent connection to a chat channel. Start blocks;
// the gateway should remain running until ctx is cancelled or Stop is
// called.
type Gateway interface {
	Name() string
	Start(ctx context.Context, router *Router) error
	Stop(ctx context.Context) error
}

// StatusReader is the read-only query surface gateway-local commands
// like /status need. The daemon supplies its store, which satisfies this
// structurally.
type StatusReader interface {
	StatusCounts(ctx context.Context) (map[string]int, error)
}

// LLMResponder routes a message to the LLM and returns the reply. When
// nil (not yet wired — see abg.13), non-command messages get a static
// "LLM not configured" response. Gateways call this for any message the
// router did not handle directly.
type LLMResponder func(ctx context.Context, msg Message) (string, error)

// ModelManager provides access to available models and allows switching the
// active LLM model. The daemon supplies an implementation backed by its
// runtime provider catalog. When nil on a Router, /model and /models
// return "not configured" messages.
type ModelManager interface {
	// Models returns all available model references in "provider/model" format.
	Models() []string
	// ActiveModel returns the currently active model reference.
	// Returns empty string when no model is active.
	ActiveModel() string
	// SetActiveModel switches the active model. Returns an error if the
	// reference is unknown.
	SetActiveModel(ctx context.Context, ref string) error
}

// Router dispatches inbound messages. Gateway-local commands (like
// /status) are handled directly. Everything else is routed to the LLM
// responder. Gateway implementations call Route on every inbound
// message.
type Router struct {
	Store       StatusReader
	Models      ModelManager // nil = /model and /models not configured
	LLM         LLMResponder // nil = LLM not wired yet
	gatewayName string
}

// NewRouter returns a Router. llm is optional — when nil, non-command
// messages get a "not configured" response.
func NewRouter(store StatusReader, llm LLMResponder, gatewayName string) *Router {
	return &Router{Store: store, LLM: llm, gatewayName: gatewayName}
}

// Route dispatches msg and returns the reply. Gateway-local commands
// (like /status, /model, /models) are handled directly; everything
// else goes to the LLM responder.
func (r *Router) Route(ctx context.Context, msg Message) (string, error) {
	text := strings.TrimSpace(msg.Text)
	cmd, arg := parseCmd(text, r.gatewayName)

	switch cmd {
	case "/status":
		return r.handleStatus(ctx)
	case "/models":
		return r.handleModels(ctx)
	case "/model":
		return r.handleModel(ctx, arg)
	default:
		if r.LLM == nil {
			return "I'm running but LLM processing isn't wired yet. Try /status.", nil
		}
		return r.LLM(ctx, msg)
	}
}

// parseCmd extracts the command name from text, stripping an optional
// @gateway mention from the command token, and returns the command and
// its first argument.
func parseCmd(text, gatewayName string) (cmd, arg string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	raw := fields[0]
	if gatewayName != "" {
		suffix := "@" + gatewayName
		raw = strings.TrimSuffix(raw, suffix)
	}
	if len(fields) > 1 {
		return raw, fields[1]
	}
	return raw, ""
}

func (r *Router) handleModels(ctx context.Context) (string, error) {
	if r.Models == nil {
		return "Model management is not configured.", nil
	}
	models := r.Models.Models()
	if len(models) == 0 {
		return "No models configured.", nil
	}
	active := r.Models.ActiveModel()

	var b strings.Builder
	b.WriteString("Available models:\n")
	for _, m := range models {
		if m == active {
			fmt.Fprintf(&b, "  %s (active)\n", m)
		} else {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	return b.String(), nil
}

func (r *Router) handleModel(ctx context.Context, arg string) (string, error) {
	if r.Models == nil {
		return "Model switching is not configured.", nil
	}
	if arg == "" {
		models := r.Models.Models()
		if len(models) == 0 {
			return "No models configured.\nUsage: /model <provider/model>", nil
		}
		var b strings.Builder
		b.WriteString("Usage: /model <provider/model>\nAvailable models:\n")
		active := r.Models.ActiveModel()
		for _, m := range models {
			if m == active {
				fmt.Fprintf(&b, "  %s (active)\n", m)
			} else {
				fmt.Fprintf(&b, "  %s\n", m)
			}
		}
		return b.String(), nil
	}
	if err := r.Models.SetActiveModel(ctx, arg); err != nil {
		return fmt.Sprintf("Cannot switch: %v", err), nil
	}
	return fmt.Sprintf("Active model set to %s.", arg), nil
}

func (r *Router) handleStatus(ctx context.Context) (string, error) {
	counts, err := r.Store.StatusCounts(ctx)
	if err != nil {
		return "", fmt.Errorf("status: %w", err)
	}
	if len(counts) == 0 {
		return "No tasks yet.", nil
	}
	statuses := make([]string, 0, len(counts))
	for s := range counts {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)

	var b strings.Builder
	b.WriteString("Task status:\n")
	for _, s := range statuses {
		fmt.Fprintf(&b, "  %s: %d\n", s, counts[s])
	}
	return b.String(), nil
}
