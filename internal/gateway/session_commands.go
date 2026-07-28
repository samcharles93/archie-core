package gateway

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// sessionTracker maps (channelID, threadID) pairs to the currently active
// session ID. When a new session is created via /new, /branch, or /topic,
// subsequent messages go to that session.
type sessionTracker struct {
	sessions SessionStore
	// active maps "channelID:threadID" → sessionID
	active map[string]string
}

func newSessionTracker(sessions SessionStore) *sessionTracker {
	return &sessionTracker{
		sessions: sessions,
		active:   make(map[string]string),
	}
}

// resolve returns the session ID for a message, creating one if needed.
// ThreadID may be empty for flat chats.
func (t *sessionTracker) resolve(ctx context.Context, platform, botUser, channelID, threadID string) (string, error) {
	key := sessionTrackerKey(channelID, threadID)
	if id, ok := t.active[key]; ok {
		return id, nil
	}

	// Look up existing sessions for this channel+thread.
	sessions, err := t.sessions.GetByChannel(ctx, platform, channelID)
	if err != nil {
		return "", fmt.Errorf("resolve session: %w", err)
	}

	// Find the most recent session matching this thread and bot.
	for _, s := range sessions {
		if s.Source.BotUser == botUser && s.Source.ThreadID == threadID {
			t.active[key] = s.SessionID
			return s.SessionID, nil
		}
	}

	// Create a new session with a deterministic key so it matches the
	// legacy sessionKey format used by the LLM responder.
	id := sessionKeyFromFields(channelID, threadID)
	now := time.Now()
	sc := SessionContext{
		SessionID: id,
		Source: SessionSource{
			Platform:  platform,
			BotUser:   botUser,
			ChannelID: channelID,
			ThreadID:  threadID,
		},
		CreatedAt:    now,
		LastActiveAt: now,
	}
	if err := t.sessions.Save(ctx, sc); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	t.active[key] = id
	return id, nil
}

func sessionKeyFromFields(channelID, threadID string) string {
	if threadID == "" {
		return channelID
	}
	return channelID + ":" + threadID
}

// setActive sets the current session for a channel+thread.
func (t *sessionTracker) setActive(channelID, threadID, sessionID string) {
	t.active[sessionTrackerKey(channelID, threadID)] = sessionID
}

// getActive returns the current session ID for a channel+thread.
func (t *sessionTracker) getActive(channelID, threadID string) string {
	return t.active[sessionTrackerKey(channelID, threadID)]
}

// parsePositiveInt is a nilerr-safe wrapper: it returns (n, true) on
// success or (0, false) when the string is not a positive integer.
func parsePositiveInt(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

func sessionTrackerKey(channelID, threadID string) string {
	if threadID == "" {
		return channelID
	}
	return channelID + ":" + threadID
}

// ResolveSessionKey resolves or creates a session for a message and
// returns the session ID. This is the entry point the LLM responder
// in main.go should call instead of its inline sessionKey helper.
func (r *Router) ResolveSessionKey(ctx context.Context, msg Message) (string, error) {
	if r.sessionTracker == nil {
		return sessionKeyFromMsg(msg), nil
	}
	return r.sessionTracker.resolve(ctx, r.gatewayName, r.Identity, msg.ChannelID, msg.ThreadID)
}

// sessionKeyFromMsg is the fallback when no session tracker is configured.
func sessionKeyFromMsg(msg Message) string {
	if msg.ThreadID != "" {
		return msg.ChannelID + ":" + msg.ThreadID
	}
	return msg.ChannelID
}

// ── Router session command handlers ────────────────────────────────────────

// handleStart acknowledges the bot is running without consuming a reply.
func (r *Router) handleStart(ctx context.Context, msg Message) (string, error) {
	return "Archie is running. Send a message to chat, or type /help for available commands.", nil
}

// handleNew creates a fresh session for the current channel+thread,
// optionally setting a title.
func (r *Router) handleNew(ctx context.Context, msg Message, rest string) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	id := uuid.NewString()
	now := time.Now()
	title := strings.TrimSpace(rest)

	sc := SessionContext{
		SessionID: id,
		Source: SessionSource{
			Platform:  r.gatewayName,
			BotUser:   r.Identity,
			ChannelID: msg.ChannelID,
			ThreadID:  msg.ThreadID,
		},
		Title:        title,
		CreatedAt:    now,
		LastActiveAt: now,
	}
	if err := r.sessionTracker.sessions.Save(ctx, sc); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	r.sessionTracker.setActive(msg.ChannelID, msg.ThreadID, id)

	if title != "" {
		return fmt.Sprintf("New session created: %s (%s)", title, id[:8]), nil
	}
	return fmt.Sprintf("New session created (%s). Conversation history has been cleared.", id[:8]), nil
}

// handleTopic manages topic/session switching for threaded conversations.
// "off" disables topic routing (reverts to flat), "help" shows info,
// and a session-id switches to that session.
func (r *Router) handleTopic(ctx context.Context, msg Message, rest string) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	switch rest {
	case "off":
		r.sessionTracker.setActive(msg.ChannelID, "", r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID))
		return "Topic routing disabled. Messages will use the General topic.", nil
	case "help":
		return topicHelpText(), nil
	case "":
		return r.topicListSessions(ctx, msg)
	default:
		return r.topicSwitchSession(ctx, msg, rest)
	}
}

func topicHelpText() string {
	return "Usage: /topic [off|help|<session-id>]\n" +
		"  off          — disable topic routing\n" +
		"  help         — show this help\n" +
		"  <session-id> — switch to a session by its first 8 characters"
}

func (r *Router) topicListSessions(ctx context.Context, msg Message) (string, error) {
	active := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	sessions, err := r.sessionTracker.sessions.GetByChannel(ctx, r.gatewayName, msg.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}

	var b strings.Builder
	b.WriteString("Sessions for this channel:\n")
	for _, s := range sessions {
		marker := " "
		if s.SessionID == active {
			marker = "*"
		}
		prefix := s.SessionID[:min(8, len(s.SessionID))]
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, " %s %s — %s", marker, prefix, title)
		if s.BranchName != "" {
			fmt.Fprintf(&b, " [branch: %s]", s.BranchName)
		}
		fmt.Fprintln(&b)
	}
	return b.String(), nil
}

func (r *Router) topicSwitchSession(ctx context.Context, msg Message, rest string) (string, error) {
	sessions, err := r.sessionTracker.sessions.GetByChannel(ctx, r.gatewayName, msg.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	for _, s := range sessions {
		if len(s.SessionID) >= len(rest) && s.SessionID[:len(rest)] == rest {
			r.sessionTracker.setActive(msg.ChannelID, msg.ThreadID, s.SessionID)
			prefix := s.SessionID[:min(8, len(s.SessionID))]
			title := s.Title
			if title == "" {
				title = "(untitled)"
			}
			return fmt.Sprintf("Switched to session %s: %s", prefix, title), nil
		}
	}
	return fmt.Sprintf("No session found matching %q. Use /topic to list available sessions.", rest), nil
}

// handleRetry removes the last assistant message from history and replays
// the last user message through the LLM.
func (r *Router) handleRetry(ctx context.Context, msg Message) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	sessionID := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	if sessionID == "" {
		return "No active session. Send a message first.", nil
	}

	// Delete the last message (assistant response).
	deleted, err := r.sessionTracker.sessions.DeleteRecentMessages(ctx, sessionID, 1)
	if err != nil {
		return "", fmt.Errorf("retry: %w", err)
	}
	if deleted == 0 {
		return "Nothing to retry — no messages in this session.", nil
	}

	// Retrieve the last user message and replay it.
	msgs, err := r.sessionTracker.sessions.RecentMessages(ctx, sessionID, 1)
	if err != nil {
		return "", fmt.Errorf("retry load messages: %w", err)
	}
	if len(msgs) == 0 {
		return "Nothing to retry — no messages remain.", nil
	}

	lastUserMsg := msgs[0]
	if r.LLM == nil {
		return "LLM is not configured. The last response has been removed; send another message to continue.", nil
	}
	return r.LLM(ctx, lastUserMsg)
}

// handleUndo removes the last N messages (default 1) from the session
// history.
func (r *Router) handleUndo(ctx context.Context, msg Message, rest string) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	sessionID := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	if sessionID == "" {
		return "No active session. Send a message first.", nil
	}

	n := 1
	if rest != "" {
		parsed, ok := parsePositiveInt(strings.TrimSpace(rest))
		if !ok {
			return "Usage: /undo [N] — N must be a positive number (default: 1)", nil
		}
		n = parsed
	}

	deleted, err := r.sessionTracker.sessions.DeleteRecentMessages(ctx, sessionID, n)
	if err != nil {
		return "", fmt.Errorf("undo: %w", err)
	}

	if deleted == 0 {
		return "Nothing to undo — no messages in this session.", nil
	}
	if deleted == 1 {
		return "Removed 1 message.", nil
	}
	return fmt.Sprintf("Removed %d messages.", deleted), nil
}

// handleTitle sets a display title on the current session.
func (r *Router) handleTitle(ctx context.Context, msg Message, rest string) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	sessionID := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	if sessionID == "" {
		return "No active session. Send a message first.", nil
	}

	title := strings.TrimSpace(rest)
	if title == "" {
		// Show current title.
		sc, err := r.sessionTracker.sessions.Get(ctx, sessionID)
		if err != nil {
			return "", fmt.Errorf("get session: %w", err)
		}
		if sc == nil || sc.Title == "" {
			return "No title set. Use /title <name> to set one.", nil
		}
		return fmt.Sprintf("Session title: %s", sc.Title), nil
	}

	sc, err := r.sessionTracker.sessions.Get(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}
	if sc == nil {
		return "Session not found.", nil
	}
	sc.Title = title
	if err := r.sessionTracker.sessions.Save(ctx, *sc); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}
	return fmt.Sprintf("Session title set to: %s", title), nil
}

// handleBranch creates a new child session that inherits the current
// session's history. The new session becomes the active session.
func (r *Router) handleBranch(ctx context.Context, msg Message, rest string) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	channelID := msg.ChannelID
	threadID := msg.ThreadID
	parentID := r.sessionTracker.getActive(channelID, threadID)

	branchName := strings.TrimSpace(rest)

	// Get parent session for title inheritance.
	parentSC, err := r.sessionTracker.sessions.Get(ctx, parentID)
	if err != nil {
		return "", fmt.Errorf("get parent session: %w", err)
	}

	id := uuid.NewString()
	now := time.Now()
	title := branchName
	if parentSC != nil && parentSC.Title != "" && branchName == "" {
		title = parentSC.Title + " (branch)"
	}
	if title == "" {
		title = "Branch of " + parentID[:8]
	}

	sc := SessionContext{
		SessionID: id,
		Source: SessionSource{
			Platform:  r.gatewayName,
			BotUser:   r.Identity,
			ChannelID: channelID,
			ThreadID:  threadID,
		},
		Title:           title,
		ParentSessionID: parentID,
		BranchName:      branchName,
		CreatedAt:       now,
		LastActiveAt:    now,
	}
	if err := r.sessionTracker.sessions.Save(ctx, sc); err != nil {
		return "", fmt.Errorf("create branch session: %w", err)
	}

	// Inherit history from parent.
	msgs, err := r.sessionTracker.sessions.RecentMessages(ctx, parentID, 200)
	if err != nil {
		return "", fmt.Errorf("inherit history: %w", err)
	}
	if len(msgs) > 0 {
		if err := r.sessionTracker.sessions.SaveMessages(ctx, id, msgs); err != nil {
			return "", fmt.Errorf("copy history: %w", err)
		}
	}

	r.sessionTracker.setActive(channelID, threadID, id)

	if branchName != "" {
		return fmt.Sprintf("Branched to \"%s\" (%s) with %d inherited messages.", branchName, id[:8], len(msgs)), nil
	}
	return fmt.Sprintf("Branched session (%s) with %d inherited messages.", id[:8], len(msgs)), nil
}

// handleCompress applies context compression to the current session's
// history. Sub-commands: here [N], focus <topic>, --preview, --dry-run.
func (r *Router) handleCompress(ctx context.Context, msg Message, rest string) (string, error) {
	if r.sessionTracker == nil {
		return "Session management is not configured.", nil
	}

	sessionID := r.sessionTracker.getActive(msg.ChannelID, msg.ThreadID)
	if sessionID == "" {
		return "No active session. Send a message first.", nil
	}

	fields := strings.Fields(rest)
	isPreview := len(fields) == 0 || fields[0] == "--preview" || fields[0] == "--dry-run"

	if isPreview {
		return r.compressPreview(ctx, sessionID)
	}

	cfg := DefaultCompressionConfig()
	if fields[0] == "here" && len(fields) > 1 {
		n, ok := parsePositiveInt(fields[1])
		if !ok {
			return "Usage: /compress here <N> — N must be a positive number", nil
		}
		cfg.ProtectLast = n
	}
	if fields[0] == "focus" && len(fields) < 2 {
		return "Usage: /compress focus <topic>", nil
	}

	return r.applyCompress(ctx, sessionID, cfg)
}

func (r *Router) compressPreview(ctx context.Context, sessionID string) (string, error) {
	cfg := DefaultCompressionConfig()
	count, err := r.sessionTracker.sessions.MessageCount(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("message count: %w", err)
	}

	msgs, err := r.sessionTracker.sessions.RecentMessages(ctx, sessionID, 200)
	if err != nil {
		return "", fmt.Errorf("load messages: %w", err)
	}

	compressed := r.messagesToCompressed(msgs)
	view := CompressHistory(compressed, cfg)

	if !view.WasCompressed {
		return fmt.Sprintf("Compression not needed (%d messages, ~%d tokens).", count, view.TokensBefore), nil
	}

	removed := count - cfg.ProtectFirst - cfg.ProtectLast
	return fmt.Sprintf(
		"Compression preview:\n"+
			"  Messages: %d\n"+
			"  Tokens before: ~%d\n"+
			"  Tokens after: ~%d\n"+
			"  %d messages would be summarised.\n"+
			"Run /compress to apply, or /compress here <N> to keep the last N messages protected.",
		count, view.TokensBefore, view.TokensAfter, removed,
	), nil
}

func (r *Router) messagesToCompressed(msgs []Message) []CompressedMessage {
	compressed := make([]CompressedMessage, 0, len(msgs))
	for _, h := range msgs {
		role := "user"
		if h.From == r.Identity {
			role = "assistant"
		}
		compressed = append(compressed, CompressedMessage{
			Role:    role,
			Content: h.Text,
		})
	}
	return compressed
}

func (r *Router) applyCompress(ctx context.Context, sessionID string, cfg CompressionConfig) (string, error) {
	msgs, err := r.sessionTracker.sessions.RecentMessages(ctx, sessionID, 200)
	if err != nil {
		return "", fmt.Errorf("load messages: %w", err)
	}

	compressed := r.messagesToCompressed(msgs)
	view := CompressHistory(compressed, cfg)
	if !view.WasCompressed {
		return fmt.Sprintf("Compression not needed (%d messages, ~%d tokens).", len(msgs), view.TokensBefore), nil
	}

	// Replace messages with the compressed view. Delete all, then
	// re-save the compressed messages.
	allCount, _ := r.sessionTracker.sessions.MessageCount(ctx, sessionID)
	_, _ = r.sessionTracker.sessions.DeleteRecentMessages(ctx, sessionID, allCount)

	compressedMsgs := make([]Message, 0, len(view.Messages))
	for _, cm := range view.Messages {
		compressedMsgs = append(compressedMsgs, Message{
			From: cm.Role,
			Text: cm.Content,
		})
	}
	if err := r.sessionTracker.sessions.SaveMessages(ctx, sessionID, compressedMsgs); err != nil {
		return "", fmt.Errorf("save compressed messages: %w", err)
	}

	return fmt.Sprintf(
		"Compressed: %d messages → %d (~%d → ~%d tokens).",
		len(msgs), len(view.Messages), view.TokensBefore, view.TokensAfter,
	), nil
}
