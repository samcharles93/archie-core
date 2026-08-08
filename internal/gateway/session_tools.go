package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/tools"
)

const (
	// defaultSessionListLimit is how many sessions a bare session_list returns.
	defaultSessionListLimit = 20

	// maxSessionListLimit bounds what the model can ask for. A chat that has
	// churned for months could otherwise land its whole session catalog in the
	// context window whole.
	maxSessionListLimit = 100

	// maxSessionTitleLen bounds the title session_title will accept. Titles are
	// rendered in command replies and the dashboard; an unbounded one would let
	// a model bloat every surface that shows it.
	maxSessionTitleLen = 80
)

// ChatSessionSummary is the per-session view the session tools return. It is
// deliberately narrower than a store session: the source tuple (platform, bot,
// channel, thread) is addressing metadata, not something that helps the model
// pick which conversation to resume.
type ChatSessionSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	BranchName   string    `json:"branch_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// SessionListResult is what session_list returns.
type SessionListResult struct {
	Sessions []ChatSessionSummary `json:"sessions"`
}

// SessionResumeResult is what session_resume returns.
type SessionResumeResult struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
}

// SessionTitleResult is what session_title returns.
type SessionTitleResult struct {
	SessionID     string `json:"session_id"`
	Title         string `json:"title"`
	PreviousTitle string `json:"previous_title"`
}

// SessionDeleteResult is what session_delete returns.
type SessionDeleteResult struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
}

// SessionTools builds the chat tools that let Archie manage its own sessions.
// Until these existed, /resume, /title and /delete were slash commands only a
// human could type, so "switch back to that conversation" had no tool path at
// all.
//
// The tools are scoped to the current platform + channel: session_list returns
// only this channel's sessions (not every session the daemon knows), and
// session_resume/session_delete resolve references against that same scoped
// list. A model in chat A cannot list or manipulate chat B's history.
//
// msg is captured so the handlers act on the chat they were built for; the
// model never supplies a channel or thread.
//
// A nil store omits every tool rather than registering ones that always fail,
// so a daemon without session support advertises nothing. A nil tracker omits
// the two tools that need it (session_resume and session_delete).
func SessionTools(store SessionStore, tracker *sessionTracker, platform string, msg Message) []tools.ToolEntry {
	if store == nil {
		return nil
	}
	channelID := msg.ChannelID
	var entries []tools.ToolEntry
	entries = append(entries, sessionListTool(store, platform, channelID))
	if tracker != nil {
		entries = append(entries, sessionResumeTool(store, tracker, platform, channelID, msg))
		entries = append(entries, sessionDeleteTool(store, tracker, platform, channelID))
	}
	entries = append(entries, sessionTitleTool(store, platform, channelID))
	return entries
}

func sessionListTool(store SessionStore, platform, channelID string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "session_list",
		Toolset: "session",
		Description: "List this chat's sessions, most recent first. " +
			"Use it to answer questions about past conversations and to find the " +
			"session_id for session_resume, session_title or session_delete.",
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum sessions to return. Defaults to %d, capped at %d.", defaultSessionListLimit, maxSessionListLimit),
				},
			},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			limit := sessionListLimit(input)

			all, err := store.GetByChannel(ctx, platform, channelID)
			if err != nil {
				return nil, fmt.Errorf("session_list: %w", err)
			}
			// GetByChannel returns newest first by contract, but sort anyway
			// so the tool never depends on a backend honouring ordering.
			slices.SortStableFunc(all, func(a, b SessionContext) int {
				return sessionRecency(b).Compare(sessionRecency(a))
			})
			if len(all) > limit {
				all = all[:limit]
			}
			// Non-nil even when empty: an empty catalog is an answer, and a
			// null would read to the model as a failed call worth retrying.
			sessions := make([]ChatSessionSummary, 0, len(all))
			for _, s := range all {
				sessions = append(sessions, ChatSessionSummary{
					ID:           s.SessionID,
					Title:        sessionDisplayTitle(s),
					BranchName:   s.BranchName,
					CreatedAt:    s.CreatedAt,
					LastActiveAt: s.LastActiveAt,
				})
			}
			return SessionListResult{Sessions: sessions}, nil
		},
	}
}

func sessionResumeTool(store SessionStore, tracker *sessionTracker, platform, channelID string, msg Message) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "session_resume",
		Toolset: "session",
		Description: "Make the named session the active one for this chat, so subsequent messages " +
			"continue that conversation. Use it when the user asks to resume or switch to a " +
			"conversation listed by session_list.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The session ID or a unique prefix, same as /resume.",
				},
			},
			"required": []any{"session_id"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			ref := strings.TrimSpace(asString(input["session_id"]))
			if ref == "" {
				return nil, fmt.Errorf("session_resume: session_id is required")
			}

			sessions, err := store.GetByChannel(ctx, platform, channelID)
			if err != nil {
				return nil, fmt.Errorf("session_resume: %w", err)
			}
			match, ambiguous := resolveSessionRef(sessions, ref)
			switch {
			case ambiguous:
				return nil, fmt.Errorf("session_resume: multiple sessions match %q; be more specific", ref)
			case match == nil:
				return nil, fmt.Errorf("session_resume: no session matching %q", ref)
			}

			// The channel+thread are the ones this tool was built for, never
			// input. Tracker presence is guaranteed: SessionTools omits the
			// tool when no tracker is wired.
			tracker.setActive(msg.ChannelID, msg.ThreadID, match.SessionID)
			return SessionResumeResult{
				SessionID: match.SessionID,
				Title:     sessionDisplayTitle(*match),
				Message: fmt.Sprintf("Resumed session %s (%s).",
					shortSessionID(match.SessionID), sessionDisplayTitle(*match)),
			}, nil
		},
	}
}

func sessionTitleTool(store SessionStore, platform, channelID string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "session_title",
		Toolset: "session",
		Description: "Set a display title on a session. Use it when the user asks to name or " +
			"rename a conversation.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The session ID, as returned by session_list.",
				},
				"title": map[string]any{
					"type":        "string",
					"description": fmt.Sprintf("New title, at most %d characters.", maxSessionTitleLen),
				},
			},
			"required": []any{"session_id", "title"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			sessionID := strings.TrimSpace(asString(input["session_id"]))
			if sessionID == "" {
				return nil, fmt.Errorf("session_title: session_id is required")
			}
			title := strings.TrimSpace(asString(input["title"]))
			if title == "" {
				return nil, fmt.Errorf("session_title: title is required")
			}
			if len([]rune(title)) > maxSessionTitleLen {
				return nil, fmt.Errorf("session_title: title must be at most %d characters", maxSessionTitleLen)
			}

			sc, err := store.Get(ctx, sessionID)
			if err != nil {
				return nil, fmt.Errorf("session_title: %w", err)
			}
			if sc == nil {
				return nil, fmt.Errorf("session_title: session %q not found", sessionID)
			}
			// Guard against cross-channel writes: the session the model named
			// must belong to the channel this turn is running in.
			if sc.Source.Platform != platform || sc.Source.ChannelID != channelID {
				return nil, fmt.Errorf("session_title: session %q does not belong to this channel", sessionID)
			}
			previous := sc.Title
			sc.Title = title
			if err := store.Save(ctx, *sc); err != nil {
				return nil, fmt.Errorf("session_title: %w", err)
			}
			return SessionTitleResult{
				SessionID:     sc.SessionID,
				Title:         title,
				PreviousTitle: previous,
			}, nil
		},
	}
}

func sessionDeleteTool(store SessionStore, tracker *sessionTracker, platform, channelID string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:    "session_delete",
		Toolset: "session",
		Description: "Permanently delete a session and its message history. " +
			"The dispatch layer asks a human to approve this before it runs.",
		Classification: tools.ClassMutating | tools.RequiresApproval,
		BuildApprovalDescription: func(input map[string]any) string {
			id := strings.TrimSpace(asString(input["session_id"]))
			return fmt.Sprintf("Permanently delete session %q and its message history. This cannot be undone.", id)
		},
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The session ID or a unique prefix, same as /delete.",
				},
			},
			"required": []any{"session_id"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			ref := strings.TrimSpace(asString(input["session_id"]))
			if ref == "" {
				return nil, fmt.Errorf("session_delete: session_id is required")
			}

			sessions, err := store.GetByChannel(ctx, platform, channelID)
			if err != nil {
				return nil, fmt.Errorf("session_delete: %w", err)
			}
			target, ambiguous := resolveSessionRef(sessions, ref)
			switch {
			case ambiguous:
				return nil, fmt.Errorf("session_delete: multiple sessions match %q; be more specific", ref)
			case target == nil:
				return nil, fmt.Errorf("session_delete: no session matching %q", ref)
			}

			if err := store.Delete(ctx, target.SessionID); err != nil {
				return nil, fmt.Errorf("session_delete: %w", err)
			}
			// Only after the store has let go of it, mirroring /delete: a
			// failed delete leaves the session live, and dropping the tracker
			// entry then would strand the operator's conversation for no
			// reason.
			tracker.forget(target.SessionID)
			return SessionDeleteResult{
				SessionID: target.SessionID,
				Title:     sessionDisplayTitle(*target),
				Message: fmt.Sprintf("Deleted session %s (%s) and its history.",
					shortSessionID(target.SessionID), sessionDisplayTitle(*target)),
			}, nil
		},
	}
}

// sessionListLimit resolves the requested limit, falling back to the default
// for anything absent or non-positive and clamping the rest, exactly like the
// task list limit.
//
// JSON numbers decode as float64, but a model that emits an integer through a
// different provider path can arrive as int, so both are accepted.
func sessionListLimit(input map[string]any) int {
	return tools.ListLimit(input, defaultSessionListLimit, maxSessionListLimit)
}
