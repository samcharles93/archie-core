// Package sessioncurator implements domain/curator.CuratorEngine for
// session memory extraction. See docs/prds/session-memory-curator.md for
// what a pass does and why, and the scope this package deliberately does
// not cover.
package sessioncurator

import (
	"context"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// Adapter implements curator.ConversationSource over a real
// gateway.SessionStore, narrowed to the two read-only operations a
// curator needs -- never the full session CRUD/branch/search surface a
// chat gateway exposes. See docs/prds/session-memory-curator.md.
type Adapter struct {
	store gateway.SessionStore
}

// NewAdapter builds an Adapter. Roles come from the canonical records the
// store returns, so no bot identity is needed to derive them.
func NewAdapter(store gateway.SessionStore) *Adapter {
	return &Adapter{store: store}
}

// RecentSessions returns sessions active at or after since, newest first.
// AgentID is the session's bot identity, the closest thing to an Agent the
// tree records today (see docs/architecture/identity.md).
func (a *Adapter) RecentSessions(ctx context.Context, since time.Time) ([]curator.SessionSummary, error) {
	sessions, err := a.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessioncurator: listing sessions: %w", err)
	}
	var out []curator.SessionSummary
	for _, s := range sessions {
		if s.LastActiveAt.Before(since) {
			continue
		}
		out = append(out, curator.SessionSummary{ID: s.SessionID, AgentID: s.Source.BotUser, LastActive: s.LastActiveAt})
	}
	return out, nil
}

// Messages returns the most recent n messages of one session, chronological.
// SenderID rides through unchanged from the stored record; deciding whose
// participant that is -- failing closed on zero or several distinct senders
// -- belongs to the curator, not this adapter.
func (a *Adapter) Messages(ctx context.Context, sessionID string, n int) ([]curator.ConversationMessage, error) {
	msgs, err := a.store.RecentMessages(ctx, sessionID, n)
	if err != nil {
		return nil, fmt.Errorf("sessioncurator: reading session %s: %w", sessionID, err)
	}
	out := make([]curator.ConversationMessage, 0, len(msgs))
	for _, m := range msgs {
		role := "user"
		if m.Role == messaging.RoleAssistant {
			role = "assistant"
		}
		out = append(out, curator.ConversationMessage{Role: role, Content: m.Text, SenderID: m.SenderID, At: m.At})
	}
	return out, nil
}
